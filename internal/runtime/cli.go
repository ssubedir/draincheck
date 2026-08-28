package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const commandOutputLimit = 4 << 20

type CLI struct {
	name   string
	binary string
	runner commandRunner
}

func Resolve(ctx context.Context, requested string) (*CLI, error) {
	if requested == "" {
		requested = "auto"
	}
	candidates := []string{requested}
	if requested == "auto" {
		candidates = []string{"docker", "podman"}
	}

	var problems []string
	for _, candidate := range candidates {
		if candidate != "docker" && candidate != "podman" {
			return nil, fmt.Errorf("runtime must be auto, docker, or podman")
		}
		binary, err := exec.LookPath(candidate)
		if err != nil {
			problems = append(problems, candidate+" executable not found")
			continue
		}
		runtime := &CLI{name: candidate, binary: binary, runner: execRunner{}}
		result := runtime.runner.Run(ctx, runtime.binary, commandOutputLimit, "info")
		if result.err != nil {
			problems = append(problems, fmt.Sprintf("%s daemon unavailable: %s", candidate, conciseError(result)))
			continue
		}
		return runtime, nil
	}
	return nil, errors.New(strings.Join(problems, "; "))
}

func (c *CLI) Name() string {
	return c.name
}

func (c *CLI) EnsureImage(ctx context.Context, image string, policy PullPolicy) error {
	if policy == PullAlways {
		return c.pull(ctx, image)
	}
	result := c.run(ctx, "image", "inspect", "--format", "{{.Id}}", image)
	if result.err == nil {
		return nil
	}
	if policy == PullMissing {
		return c.pull(ctx, image)
	}
	if policy != PullNever {
		return fmt.Errorf("unsupported pull policy %q", policy)
	}
	return fmt.Errorf("image %q is not available locally: %s", image, conciseError(result))
}

func (c *CLI) pull(ctx context.Context, image string) error {
	result := c.run(ctx, "pull", image)
	if result.err != nil {
		return fmt.Errorf("pull image %q: %s", image, conciseError(result))
	}
	return nil
}

func (c *CLI) Create(ctx context.Context, spec ContainerSpec) (Container, error) {
	args := createArgs(spec)
	result := c.run(ctx, args...)
	if result.err != nil {
		return Container{}, fmt.Errorf("create container: %s", conciseError(result))
	}
	id := strings.TrimSpace(string(result.stdout))
	if id == "" {
		return Container{}, errors.New("create container: runtime returned an empty container ID")
	}
	return Container{ID: id, Name: spec.Name}, nil
}

func createArgs(spec ContainerSpec) []string {
	args := []string{
		"create",
		"--name", spec.Name,
		"--label", "io.draincheck.run=" + spec.RunID,
	}
	ports := append([]int(nil), spec.ContainerPorts...)
	sort.Ints(ports)
	previousPort := 0
	for _, port := range ports {
		if port == previousPort {
			continue
		}
		args = append(args, "--publish", fmt.Sprintf("127.0.0.1::%d", port))
		previousPort = port
	}
	hosts := make([]string, 0, len(spec.HostAliases))
	for host := range spec.HostAliases {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		args = append(args, "--add-host", host+":"+spec.HostAliases[host])
	}
	keys := make([]string, 0, len(spec.Environment))
	for key := range spec.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--env", key+"="+spec.Environment[key])
	}
	return append(args, spec.Image)
}

func (c *CLI) Start(ctx context.Context, id string) error {
	result := c.run(ctx, "start", id)
	if result.err != nil {
		return fmt.Errorf("start container: %s", conciseError(result))
	}
	return nil
}

func (c *CLI) HostPort(ctx context.Context, id string, containerPort int) (int, error) {
	result := c.run(ctx, "port", id, fmt.Sprintf("%d/tcp", containerPort))
	if result.err != nil {
		return 0, fmt.Errorf("resolve host port for container port %d: %s", containerPort, conciseError(result))
	}
	port, err := parseHostPort(string(result.stdout))
	if err != nil {
		return 0, fmt.Errorf("resolve host port for container port %d: %w", containerPort, err)
	}
	return port, nil
}

func (c *CLI) Exec(ctx context.Context, id string, command []string, outputLimit int64) (ExecResult, error) {
	if len(command) == 0 {
		return ExecResult{}, errors.New("exec in container: command is empty")
	}
	if outputLimit <= 0 || outputLimit > commandOutputLimit {
		return ExecResult{}, fmt.Errorf("exec in container: output limit must be between 1 and %d bytes", commandOutputLimit)
	}
	args := make([]string, 0, len(command)+2)
	args = append(args, "exec", id)
	args = append(args, command...)
	commandResult := c.runner.Run(ctx, c.binary, outputLimit, args...)
	result := ExecResult{
		Stdout: append([]byte(nil), commandResult.stdout...),
		Stderr: append([]byte(nil), commandResult.stderr...),
	}
	if commandResult.err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitError interface{ ExitCode() int }
	if errors.As(commandResult.err, &exitError) {
		result.ExitCode = exitError.ExitCode()
		return result, nil
	}
	return result, fmt.Errorf("exec in container: %s", conciseError(commandResult))
}

func parseHostPort(output string) (int, error) {
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(output), "\n")[0])
	if line == "" {
		return 0, errors.New("runtime returned an empty port mapping")
	}
	_, portText, err := net.SplitHostPort(line)
	if err != nil {
		index := strings.LastIndex(line, ":")
		if index < 0 {
			return 0, fmt.Errorf("invalid port mapping %q", line)
		}
		portText = line[index+1:]
	}
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port mapping %q", line)
	}
	return port, nil
}

func (c *CLI) Signal(ctx context.Context, id string, signal string) error {
	result := c.run(ctx, "kill", "--signal", signal, id)
	if result.err != nil {
		return fmt.Errorf("signal container: %s", conciseError(result))
	}
	return nil
}

func (c *CLI) Inspect(ctx context.Context, id string) (ContainerState, error) {
	result := c.run(ctx, "container", "inspect", "--format", "{{json .State}}", id)
	if result.err != nil {
		return ContainerState{}, fmt.Errorf("inspect container: %s", conciseError(result))
	}
	var state ContainerState
	if err := json.Unmarshal(result.stdout, &state); err != nil {
		return ContainerState{}, fmt.Errorf("decode container state: %w", err)
	}
	return state, nil
}

func (c *CLI) Logs(ctx context.Context, id string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, nil
	}
	result := c.runner.Run(ctx, c.binary, limit, "logs", "--timestamps", "--tail", "2000", id)
	if result.err != nil {
		return nil, fmt.Errorf("read container logs: %s", conciseError(result))
	}
	logs := append([]byte(nil), result.stdout...)
	if len(result.stderr) > 0 {
		logs = append(logs, result.stderr...)
	}
	if int64(len(logs)) > limit {
		logs = logs[:limit]
	}
	return logs, nil
}

func (c *CLI) Remove(ctx context.Context, id string, force bool) error {
	args := removeArgs(c.name, id, force)
	result := c.run(ctx, args...)
	if result.err != nil {
		message := strings.ToLower(string(result.stderr))
		if strings.Contains(message, "no such container") || strings.Contains(message, "no container with name") {
			return nil
		}
		return fmt.Errorf("remove container: %s", conciseError(result))
	}
	return nil
}

func removeArgs(runtimeName, id string, force bool) []string {
	args := []string{"rm"}
	if force {
		args = append(args, "--force")
		if runtimeName == "podman" {
			args = append(args, "--time", "0")
		}
	}
	return append(args, id)
}

func (c *CLI) run(ctx context.Context, args ...string) commandResult {
	return c.runner.Run(ctx, c.binary, commandOutputLimit, args...)
}

func conciseError(result commandResult) string {
	message := strings.TrimSpace(string(result.stderr))
	if message == "" {
		message = strings.TrimSpace(string(result.stdout))
	}
	if message == "" && result.err != nil {
		message = result.err.Error()
	}
	if len(message) > 500 {
		message = message[:500] + "…"
	}
	return message
}
