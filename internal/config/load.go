package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v4"
)

const maxConfigBytes = 1 << 20
const maxRequestBodyBytes = 1 << 20
const maxGRPCDescriptorSetBytes = 8 << 20

func LoadFile(path string) (Config, error) {
	return LoadFileWithProfile(path, ProfileGeneric)
}

func LoadFileWithProfile(path string, profile Profile) (Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("config exceeds %d bytes", maxConfigBytes)
	}
	cfg, err := DecodeWithProfile(bytes.NewReader(data), profile)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.resolveRequestBody(filepath.Dir(path)); err != nil {
		return Config{}, err
	}
	if err := cfg.resolveCommand(filepath.Dir(path)); err != nil {
		return Config{}, err
	}
	if err := cfg.resolveGRPC(filepath.Dir(path)); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) resolveGRPC(configDirectory string) error {
	if c.Traffic.Driver == TrafficDriverGRPC {
		request, resolved, err := resolveBoundedFile(configDirectory, c.Traffic.GRPC.RequestFile, maxRequestBodyBytes, "traffic gRPC request")
		if err != nil {
			return err
		}
		c.Traffic.GRPC.requestBytes = request
		c.Traffic.GRPC.requestResolved = resolved
		if resolved && !json.Valid(request) {
			return errors.New("traffic gRPC request file must contain valid JSON")
		}
		descriptor, resolved, err := resolveBoundedFile(configDirectory, c.Traffic.GRPC.DescriptorSet, maxGRPCDescriptorSetBytes, "traffic gRPC descriptor set")
		if err != nil {
			return err
		}
		c.Traffic.GRPC.descriptorBytes = descriptor
		c.Traffic.GRPC.descriptorReady = resolved
	}
	if c.Streaming.GRPC.Enabled {
		request, resolved, err := resolveBoundedFile(configDirectory, c.Streaming.GRPC.RequestFile, maxRequestBodyBytes, "streaming gRPC request")
		if err != nil {
			return err
		}
		c.Streaming.GRPC.requestBytes = request
		c.Streaming.GRPC.requestResolved = resolved
		if resolved && !json.Valid(request) {
			return errors.New("streaming gRPC request file must contain valid JSON")
		}
		descriptor, resolved, err := resolveBoundedFile(configDirectory, c.Streaming.GRPC.DescriptorSet, maxGRPCDescriptorSetBytes, "streaming gRPC descriptor set")
		if err != nil {
			return err
		}
		c.Streaming.GRPC.descriptorBytes = descriptor
		c.Streaming.GRPC.descriptorReady = resolved
	}
	return nil
}

func resolveBoundedFile(configDirectory, configuredPath string, limit int, description string) ([]byte, bool, error) {
	configuredPath = strings.TrimSpace(configuredPath)
	if configuredPath == "" {
		return nil, false, nil
	}
	path := configuredPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(configDirectory, path)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, false, fmt.Errorf("read %s file: %w", description, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, fmt.Errorf("read %s file: %w", description, err)
	}
	if len(data) > limit {
		return nil, false, fmt.Errorf("%s file exceeds %d bytes", description, limit)
	}
	return data, true, nil
}

func (c *Config) resolveCommand(configDirectory string) error {
	if c.Traffic.Driver != TrafficDriverCommand {
		return nil
	}
	baseDirectory, err := filepath.Abs(configDirectory)
	if err != nil {
		return fmt.Errorf("resolve traffic command config directory: %w", err)
	}
	directory := baseDirectory
	if configured := strings.TrimSpace(c.Traffic.Command.WorkingDirectory); configured != "" {
		directory = configured
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(baseDirectory, directory)
		}
	}
	directory = filepath.Clean(directory)
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("resolve traffic command working directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("resolve traffic command working directory: path is not a directory")
	}

	executable := strings.TrimSpace(c.Traffic.Command.Executable)
	if executable == "" {
		return nil
	}
	if filepath.IsAbs(executable) || strings.ContainsAny(executable, `/\\`) {
		if !filepath.IsAbs(executable) {
			executable = filepath.Join(baseDirectory, executable)
		}
		executable = filepath.Clean(executable)
		info, err := os.Stat(executable)
		if err != nil {
			return fmt.Errorf("resolve traffic command executable: %w", err)
		}
		if info.IsDir() {
			return errors.New("resolve traffic command executable: path is a directory")
		}
	} else {
		resolved, err := exec.LookPath(executable)
		if err != nil {
			return fmt.Errorf("resolve traffic command executable: %w", err)
		}
		executable, err = filepath.Abs(resolved)
		if err != nil {
			return fmt.Errorf("resolve traffic command executable: %w", err)
		}
	}
	c.Traffic.Command.resolvedExecutable = executable
	c.Traffic.Command.resolvedDirectory = directory
	c.Traffic.Command.resolved = true
	return nil
}

func Decode(reader io.Reader) (Config, error) {
	return DecodeWithProfile(reader, ProfileGeneric)
}

func DecodeWithProfile(reader io.Reader, profile Profile) (Config, error) {
	cfg, err := DefaultsForProfile(profile)
	if err != nil {
		return Config{}, err
	}
	decoder := yaml.NewDecoder(io.LimitReader(reader, maxConfigBytes+1))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: multiple YAML documents are not supported")
		}
		return Config{}, fmt.Errorf("decode trailing config data: %w", err)
	}

	return cfg, nil
}

func (c *Config) resolveRequestBody(configDirectory string) error {
	bodyFile := strings.TrimSpace(c.Traffic.Request.BodyFile)
	if bodyFile == "" || c.Traffic.Request.Body != "" {
		return nil
	}
	path := bodyFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(configDirectory, path)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("read traffic request body file: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxRequestBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read traffic request body file: %w", err)
	}
	if len(data) > maxRequestBodyBytes {
		return fmt.Errorf("traffic request body file exceeds %d bytes", maxRequestBodyBytes)
	}
	c.Traffic.Request.bodyBytes = data
	c.Traffic.Request.bodyResolved = true
	return nil
}
