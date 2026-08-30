package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)

type PackageOptions struct {
	Root          string
	OutputDir     string
	Version       string
	Commit        string
	BuildDate     time.Time
	Architectures []string
	GoBinary      string
}

type archiveEntry struct {
	name string
	path string
	mode int64
}

func Package(ctx context.Context, options PackageOptions) ([]string, error) {
	if err := validatePackageOptions(options); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	releaseOutput, err := filepath.Abs(options.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve release output: %w", err)
	}
	if err := os.MkdirAll(releaseOutput, 0o755); err != nil { // #nosec G301 -- Release artifacts are intentionally shareable.
		return nil, fmt.Errorf("create release output: %w", err)
	}
	temporary, err := os.MkdirTemp(releaseOutput, ".draincheck-release-*")
	if err != nil {
		return nil, fmt.Errorf("create release workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporary) }()

	goBinary := options.GoBinary
	if goBinary == "" {
		goBinary = "go"
	}
	buildDate := options.BuildDate.UTC().Format(time.RFC3339)
	artifacts := make([]string, 0, len(options.Architectures))
	for _, architecture := range options.Architectures {
		binaryDir := filepath.Join(temporary, architecture)
		if err := os.MkdirAll(binaryDir, 0o755); err != nil { // #nosec G301 -- Packaged binaries require traversal during archiving.
			return nil, fmt.Errorf("create %s build directory: %w", architecture, err)
		}
		binaryPath := filepath.Join(binaryDir, "draincheck")
		ldflags := strings.Join([]string{
			"-s",
			"-w",
			"-X", "main.version=" + options.Version,
			"-X", "main.commit=" + options.Commit,
			"-X", "main.date=" + buildDate,
		}, " ")
		command := exec.CommandContext(ctx, goBinary, // #nosec G204 -- The caller may override the Go tool for release tests.
			"build",
			"-buildvcs=false",
			"-trimpath",
			"-ldflags", ldflags,
			"-o", binaryPath,
			"./cmd/draincheck",
		)
		command.Dir = root
		command.Env = buildEnvironment(os.Environ(), architecture)
		buildOutput, buildErr := command.CombinedOutput()
		if buildErr != nil {
			return nil, fmt.Errorf("build linux/%s binary: %w: %s", architecture, buildErr, strings.TrimSpace(string(buildOutput)))
		}

		entries, err := packageEntries(root, binaryPath)
		if err != nil {
			return nil, err
		}

		archivePath := filepath.Join(releaseOutput, fmt.Sprintf("draincheck_linux_%s.tar.gz", architecture))
		if err := writeArchive(archivePath, entries, options.BuildDate.UTC()); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, archivePath)
	}
	return artifacts, nil
}

func packageEntries(root, binaryPath string) ([]archiveEntry, error) {
	entries := []archiveEntry{
		{name: "README.md", path: filepath.Join(root, "README.md"), mode: 0o644},
		{name: "docs/support.md", path: filepath.Join(root, "documentation", "content", "docs", "support.md"), mode: 0o644},
		{name: "draincheck", path: binaryPath, mode: 0o755},
		{name: "schema/draincheck.schema.json", path: filepath.Join(root, "schema", "draincheck.schema.json"), mode: 0o644},
	}
	licensePath := filepath.Join(root, "LICENSE")
	if _, statErr := os.Stat(licensePath); statErr != nil {
		return nil, fmt.Errorf("require release license: %w", statErr)
	}
	entries = append(entries, archiveEntry{name: "LICENSE", path: licensePath, mode: 0o644})
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return entries, nil
}

func WriteChecksums(outputDir string) (string, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return "", fmt.Errorf("read release output: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasPrefix(entry.Name(), "draincheck_") && !strings.HasSuffix(entry.Name(), ".sigstore.json") {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return "", errors.New("release output contains no draincheck artifacts")
	}
	sort.Strings(names)
	var checksums strings.Builder
	for _, name := range names {
		path := filepath.Join(outputDir, name)
		file, err := os.Open(path) // #nosec G304 -- The filename comes from the selected release output directory.
		if err != nil {
			return "", fmt.Errorf("open release artifact %q: %w", name, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash release artifact %q: %w", name, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close release artifact %q: %w", name, closeErr)
		}
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(hash.Sum(nil)), name)
	}
	path := filepath.Join(outputDir, "SHA256SUMS")
	if err := writeAtomic(path, strings.NewReader(checksums.String()), 0o644); err != nil {
		return "", fmt.Errorf("write release checksums: %w", err)
	}
	return path, nil
}

func validatePackageOptions(options PackageOptions) error {
	if !versionPattern.MatchString(options.Version) {
		return fmt.Errorf("version %q must be a v-prefixed semantic version without build metadata", options.Version)
	}
	if strings.TrimSpace(options.Commit) == "" || strings.ContainsAny(options.Commit, " \t\r\n") {
		return errors.New("commit must be a non-empty value without whitespace")
	}
	if options.BuildDate.IsZero() {
		return errors.New("build date is required")
	}
	if options.Root == "" {
		return errors.New("repository root is required")
	}
	if options.OutputDir == "" {
		return errors.New("release output directory is required")
	}
	if len(options.Architectures) == 0 {
		return errors.New("at least one architecture is required")
	}
	seen := make(map[string]bool, len(options.Architectures))
	for _, architecture := range options.Architectures {
		if architecture != "amd64" && architecture != "arm64" {
			return fmt.Errorf("unsupported release architecture %q", architecture)
		}
		if seen[architecture] {
			return fmt.Errorf("duplicate release architecture %q", architecture)
		}
		seen[architecture] = true
	}
	return nil
}

func buildEnvironment(environment []string, architecture string) []string {
	overrides := map[string]string{
		"CGO_ENABLED": "0",
		"GOARCH":      architecture,
		"GOOS":        "linux",
	}
	result := make([]string, 0, len(environment)+len(overrides))
	for _, item := range environment {
		name, _, found := strings.Cut(item, "=")
		if found {
			if _, overridden := overrides[strings.ToUpper(name)]; overridden {
				continue
			}
		}
		result = append(result, item)
	}
	for _, name := range []string{"CGO_ENABLED", "GOARCH", "GOOS"} {
		result = append(result, name+"="+overrides[name])
	}
	return result
}

func writeArchive(path string, entries []archiveEntry, timestamp time.Time) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil { // #nosec G301 -- Release artifacts are intentionally shareable.
		return fmt.Errorf("create archive directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".draincheck-archive-*")
	if err != nil {
		return fmt.Errorf("create temporary archive: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set archive permissions: %w", err)
	}

	gzipWriter, err := gzip.NewWriterLevel(temporary, gzip.BestCompression)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.ModTime = timestamp
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		info, err := os.Stat(entry.path)
		if err != nil {
			closeArchiveWriters(tarWriter, gzipWriter, temporary)
			return fmt.Errorf("inspect archive input %q: %w", entry.name, err)
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     info.Size(),
			ModTime:  timestamp,
			Typeflag: tar.TypeReg,
			Format:   tar.FormatUSTAR,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			closeArchiveWriters(tarWriter, gzipWriter, temporary)
			return fmt.Errorf("write archive header %q: %w", entry.name, err)
		}
		file, err := os.Open(entry.path)
		if err != nil {
			closeArchiveWriters(tarWriter, gzipWriter, temporary)
			return fmt.Errorf("open archive input %q: %w", entry.name, err)
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		if copyErr != nil {
			closeArchiveWriters(tarWriter, gzipWriter, temporary)
			return fmt.Errorf("write archive input %q: %w", entry.name, copyErr)
		}
		if closeErr != nil {
			closeArchiveWriters(tarWriter, gzipWriter, temporary)
			return fmt.Errorf("close archive input %q: %w", entry.name, closeErr)
		}
	}
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		_ = temporary.Close()
		return fmt.Errorf("close tar archive: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("close gzip archive: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace archive %q: %w", path, err)
	}
	return nil
}

func closeArchiveWriters(tarWriter *tar.Writer, gzipWriter *gzip.Writer, file *os.File) {
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	_ = file.Close()
}

func writeAtomic(path string, reader io.Reader, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil { // #nosec G301 -- Release artifacts are intentionally shareable.
		return err
	}
	temporary, err := os.CreateTemp(directory, ".draincheck-release-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := io.Copy(temporary, reader); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return replaceFile(temporaryPath, path)
}

func replaceFile(source, destination string) error {
	if err := os.Rename(source, destination); err == nil {
		return nil
	}
	if _, err := os.Stat(destination); err != nil {
		return os.Rename(source, destination)
	}
	if err := os.Remove(destination); err != nil {
		return err
	}
	return os.Rename(source, destination)
}
