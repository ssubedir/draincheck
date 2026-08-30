package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestConfigurationSchemaV1Contract(t *testing.T) {
	if CurrentVersion != 1 {
		t.Fatalf("configuration version = %d, want stable v1", CurrentVersion)
	}
	generated, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(generated, &document); err != nil {
		t.Fatal(err)
	}
	const schemaID = "https://raw.githubusercontent.com/ssubedir/draincheck/main/schema/draincheck.schema.json"
	if document.ID != schemaID {
		t.Fatalf("schema ID = %q, want %q", document.ID, schemaID)
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "schema", "draincheck.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	committed = bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(generated, committed) {
		t.Fatal("committed JSON Schema has drifted from configuration version 1")
	}
}

func TestDogfoodConfigIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "services", "good-http", "draincheck.yaml")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Target.Image != "draincheck-good:dogfood" {
		t.Errorf("dogfood image = %q, want draincheck-good:dogfood", cfg.Target.Image)
	}
	if cfg.Traffic.Request.Path != "/work?delay=2s" {
		t.Errorf("dogfood request path = %q, want coordinated slow work", cfg.Traffic.Request.Path)
	}
}

func TestDecodeUsesDefaultsAndHonorsFalse(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 9090
assertions:
  forbid_force_kill: false
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Target.ContainerPort != 9090 {
		t.Fatalf("container port = %d", cfg.Target.ContainerPort)
	}
	if cfg.Readiness.ContainerPort != nil || cfg.Traffic.ContainerPort != nil || cfg.Streaming.SSE.ContainerPort != nil || cfg.Streaming.WebSocket.ContainerPort != nil || cfg.Streaming.GRPC.ContainerPort != nil {
		t.Fatal("optional probe ports did not default to the target port")
	}
	if cfg.ReadinessPort() != 9090 || cfg.TrafficPort() != 9090 || cfg.SSEPort() != 9090 || cfg.WebSocketPort() != 9090 || cfg.GRPCStreamPort() != 9090 {
		t.Fatal("effective probe ports did not inherit target.container_port")
	}
	if cfg.Readiness.StartupTimeout.Value() != 20*time.Second {
		t.Fatalf("startup timeout = %s", cfg.Readiness.StartupTimeout)
	}
	if cfg.Readiness.Driver != ReadinessDriverHTTP || cfg.Readiness.GRPC.Service != "" {
		t.Fatalf("readiness defaults = %#v", cfg.Readiness)
	}
	if cfg.Assertions.ForbidForceKill {
		t.Fatal("explicit false was replaced by the default")
	}
	if cfg.Traffic.PostSignal.Policy != PostSignalDisabled || cfg.Traffic.PostSignal.Count != 1 {
		t.Fatalf("post-signal defaults = %#v", cfg.Traffic.PostSignal)
	}
	if cfg.Traffic.Driver != TrafficDriverHTTP {
		t.Fatalf("traffic driver default = %q, want http", cfg.Traffic.Driver)
	}
	if len(cfg.Traffic.GRPC.ExpectedCodes) != 1 || cfg.Traffic.GRPC.ExpectedCodes[0] != "OK" {
		t.Fatalf("gRPC traffic defaults = %#v", cfg.Traffic.GRPC)
	}
	if cfg.Traffic.Request.Body != "" || cfg.Traffic.Request.BodyFile != "" || len(cfg.Traffic.Request.SuccessStatuses) != 0 {
		t.Fatalf("rich request defaults = %#v", cfg.Traffic.Request)
	}
	if cfg.Streaming.SSE.Enabled || cfg.Streaming.SSE.Path != "/events" || cfg.Streaming.SSE.InitialEvent != "ready" || cfg.Streaming.SSE.TerminalEvent != "shutdown" || cfg.Streaming.SSE.EstablishTimeout.Value() != 2*time.Second || cfg.Streaming.SSE.CloseTimeout.Value() != 5*time.Second {
		t.Fatalf("SSE streaming defaults = %#v", cfg.Streaming.SSE)
	}
	if cfg.Streaming.WebSocket.Enabled || cfg.Streaming.WebSocket.Path != "/ws" || cfg.Streaming.WebSocket.TerminalMessage != "shutdown" || cfg.Streaming.WebSocket.CloseCode != 1000 || cfg.Streaming.WebSocket.EstablishTimeout.Value() != 2*time.Second || cfg.Streaming.WebSocket.CloseTimeout.Value() != 5*time.Second {
		t.Fatalf("WebSocket streaming defaults = %#v", cfg.Streaming.WebSocket)
	}
	if cfg.Streaming.GRPC.Enabled || cfg.Streaming.GRPC.MinimumMessages != 1 || cfg.Streaming.GRPC.ExpectedCode != "OK" || cfg.Streaming.GRPC.EstablishTimeout.Value() != 2*time.Second || cfg.Streaming.GRPC.CloseTimeout.Value() != 5*time.Second {
		t.Fatalf("gRPC streaming defaults = %#v", cfg.Streaming.GRPC)
	}
	if cfg.Telemetry.Traces.Enabled || cfg.Telemetry.Traces.MinimumCorrelatedSpans != 1 || cfg.Telemetry.Traces.FlushTimeout.Value() != 2*time.Second {
		t.Fatalf("trace telemetry defaults = %#v", cfg.Telemetry.Traces)
	}
	if cfg.Telemetry.Metrics.Enabled || cfg.Telemetry.Metrics.MinimumDataPoints != 1 || cfg.Telemetry.Metrics.FlushTimeout.Value() != 2*time.Second {
		t.Fatalf("metric telemetry defaults = %#v", cfg.Telemetry.Metrics)
	}
	if !cfg.Repeat.Budgets.StartupReadyP95.IsZero() || !cfg.Repeat.Budgets.ReadinessWithdrawalP95.IsZero() || !cfg.Repeat.Budgets.ContainerExitP95.IsZero() {
		t.Fatalf("repeat budget defaults = %#v", cfg.Repeat.Budgets)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
}

func TestKubernetesProfileDefaultsAndExplicitOverrides(t *testing.T) {
	profile, err := ParseProfile("KUBERNETES")
	if err != nil || profile != ProfileKubernetes {
		t.Fatalf("ParseProfile = %q, %v", profile, err)
	}
	cfg, err := DecodeWithProfile(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
`), profile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shutdown.Deadline.Value() != 30*time.Second || cfg.Shutdown.Signal != "SIGTERM" {
		t.Fatalf("Kubernetes shutdown defaults = %#v", cfg.Shutdown)
	}

	cfg, err = DecodeWithProfile(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
shutdown:
  deadline: 7s
`), profile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shutdown.Deadline.Value() != 7*time.Second {
		t.Fatalf("explicit shutdown deadline = %s, want 7s", cfg.Shutdown.Deadline)
	}
	if _, err := ParseProfile("nomad"); err == nil {
		t.Fatal("unsupported profile was accepted")
	}
}

func TestDecodeContainerExecPreStopHook(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
shutdown:
  pre_stop:
    exec:
      command: ["/app/pre-stop", "--drain"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Shutdown.PreStop == nil || !slices.Equal(cfg.Shutdown.PreStop.Exec.Command, []string{"/app/pre-stop", "--drain"}) {
		t.Fatalf("pre-stop hook = %#v", cfg.Shutdown.PreStop)
	}
}

func TestValidateRejectsInvalidPreStopHook(t *testing.T) {
	for _, command := range [][]string{nil, {"  "}, {"/app/pre-stop", "bad\x00argument"}} {
		cfg := Default()
		cfg.Shutdown.PreStop = &PreStopHook{Exec: ExecHook{Command: command}}
		err := cfg.Validate(false)
		if err == nil || !strings.Contains(err.Error(), "shutdown.pre_stop.exec.command") {
			t.Fatalf("command %q validation error = %v", command, err)
		}
	}
}

func TestProbePortsOverrideTargetAndAreDeduplicated(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
readiness:
  container_port: 8081
traffic:
  container_port: 50051
streaming:
  sse:
    enabled: true
    container_port: 8082
  websocket:
    enabled: true
    container_port: 8082
  grpc:
    enabled: true
    container_port: 50052
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReadinessPort() != 8081 || cfg.TrafficPort() != 50051 || cfg.SSEPort() != 8082 || cfg.WebSocketPort() != 8082 || cfg.GRPCStreamPort() != 50052 {
		t.Fatal("effective probe ports did not honor overrides")
	}
	want := []int{8081, 8082, 50051, 50052}
	if got := cfg.RequiredContainerPorts(); !slices.Equal(got, want) {
		t.Fatalf("required container ports = %v, want %v", got, want)
	}
}

func TestValidateRejectsInvalidOptionalProbePorts(t *testing.T) {
	cfg := Default()
	cfg.Readiness.ContainerPort = portPointer(0)
	cfg.Traffic.ContainerPort = portPointer(65536)
	cfg.Streaming.SSE.ContainerPort = portPointer(-2)
	cfg.Streaming.WebSocket.ContainerPort = portPointer(65537)
	cfg.Streaming.GRPC.ContainerPort = portPointer(-3)
	err := cfg.Validate(false)
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, name := range []string{"readiness.container_port", "traffic.container_port", "streaming.sse.container_port", "streaming.websocket.container_port", "streaming.grpc.container_port"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not contain %q", err, name)
		}
	}
}

func TestDecodeGRPCReadiness(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
readiness:
  driver: grpc
  container_port: 50051
  grpc:
    service: example.jobs.v1.Worker
  startup_timeout: 10s
  interval: 100ms
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Readiness.Driver != ReadinessDriverGRPC || cfg.ReadinessPort() != 50051 || cfg.Readiness.GRPC.Service != "example.jobs.v1.Worker" {
		t.Fatalf("gRPC readiness = %#v", cfg.Readiness)
	}
}

func TestDecodeExecReadinessDoesNotRequireAReadinessPort(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
readiness:
  driver: exec
  exec:
    command: ["/app/healthcheck", "--ready"]
  startup_timeout: 10s
  interval: 100ms
traffic:
  container_port: 9090
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Readiness.Driver != ReadinessDriverExec || !slices.Equal(cfg.Readiness.Exec.Command, []string{"/app/healthcheck", "--ready"}) {
		t.Fatalf("exec readiness = %#v", cfg.Readiness)
	}
	if got := cfg.RequiredContainerPorts(); !slices.Equal(got, []int{9090}) {
		t.Fatalf("required container ports = %v, want traffic port only", got)
	}
}

func TestValidateRejectsInvalidReadinessConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "driver", configure: func(cfg *Config) { cfg.Readiness.Driver = "tcp" }, contains: "readiness.driver"},
		{name: "service whitespace", configure: func(cfg *Config) {
			cfg.Readiness.Driver = ReadinessDriverGRPC
			cfg.Readiness.GRPC.Service = " example.Service"
		}, contains: "readiness.grpc.service"},
		{name: "service control character", configure: func(cfg *Config) {
			cfg.Readiness.Driver = ReadinessDriverGRPC
			cfg.Readiness.GRPC.Service = "example.Service\nother"
		}, contains: "readiness.grpc.service"},
		{name: "missing exec command", configure: func(cfg *Config) {
			cfg.Readiness.Driver = ReadinessDriverExec
		}, contains: "readiness.exec.command"},
		{name: "empty exec executable", configure: func(cfg *Config) {
			cfg.Readiness.Driver = ReadinessDriverExec
			cfg.Readiness.Exec.Command = []string{"  "}
		}, contains: "readiness.exec.command[0]"},
		{name: "exec readiness port", configure: func(cfg *Config) {
			cfg.Readiness.Driver = ReadinessDriverExec
			cfg.Readiness.ContainerPort = portPointer(8081)
			cfg.Readiness.Exec.Command = []string{"/healthcheck"}
		}, contains: "readiness.container_port"},
		{name: "exec options with HTTP driver", configure: func(cfg *Config) {
			cfg.Readiness.Exec.Command = []string{"/healthcheck"}
		}, contains: "readiness.exec"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.configure(&cfg)
			err := cfg.Validate(false)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func portPointer(port int) *int { return &port }

func TestLoadFileResolvesCommandRelativeToConfig(t *testing.T) {
	directory := t.TempDir()
	probeName := "probe"
	if runtime.GOOS == "windows" {
		probeName += ".exe"
	}
	probePath := filepath.Join(directory, probeName)
	if err := os.WriteFile(probePath, []byte("fixture"), 0o700); err != nil { // #nosec G306 -- The fixture must be executable.
		t.Fatal(err)
	}
	workingPath := filepath.Join(directory, "work")
	if err := os.Mkdir(workingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "draincheck.yaml")
	content := fmt.Sprintf(`
version: 1
target:
  image: example:test
  container_port: 8080
traffic:
  driver: command
  command:
    executable: ./%s
    args: [--mode, active]
    environment:
      PROBE_TOKEN: example
    working_directory: ./work
`, probeName)
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	command := cfg.Traffic.Command
	if command.ResolvedExecutable() != probePath || command.ResolvedDirectory() != workingPath {
		t.Fatalf("resolved command = executable %q, directory %q", command.ResolvedExecutable(), command.ResolvedDirectory())
	}
}

func TestValidateRejectsInvalidCommandTraffic(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "driver", configure: func(cfg *Config) { cfg.Traffic.Driver = "script" }, contains: "traffic.driver"},
		{name: "HTTP command", configure: func(cfg *Config) { cfg.Traffic.Command.Executable = "probe" }, contains: "only valid"},
		{name: "missing executable", configure: func(cfg *Config) { cfg.Traffic.Driver = TrafficDriverCommand }, contains: "executable is required"},
		{name: "unresolved", configure: func(cfg *Config) {
			cfg.Traffic.Driver = TrafficDriverCommand
			cfg.Traffic.Command.Executable = "probe"
		}, contains: "was not resolved"},
		{name: "reserved environment", configure: func(cfg *Config) {
			cfg.Traffic.Command.Environment = map[string]string{"draincheck_target_url": "override"}
		}, contains: "reserved DRAINCHECK_"},
		{name: "invalid environment", configure: func(cfg *Config) {
			cfg.Traffic.Command.Environment = map[string]string{"BAD=KEY": "value"}
		}, contains: "invalid key"},
		{name: "trace correlation", configure: func(cfg *Config) {
			cfg.Traffic.Driver = TrafficDriverCommand
			cfg.Telemetry.Traces.Enabled = true
		}, contains: "trace correlation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeGRPCTrafficAndStreaming(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
traffic:
  driver: grpc
  grpc:
    method: example.jobs.v1.Worker/Run
    request: '{"job_id":"draincheck"}'
    metadata:
      authorization: Bearer example
    expected_codes: [OK]
streaming:
  grpc:
    enabled: true
    method: example.jobs.v1.Worker/Watch
    request: '{"job_id":"draincheck"}'
    metadata:
      x-tenant: test
    minimum_messages: 2
    expected_code: UNAVAILABLE
    establish_timeout: 750ms
    close_timeout: 3s
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Traffic.Driver != TrafficDriverGRPC || cfg.Traffic.GRPC.Method != "example.jobs.v1.Worker/Run" || string(cfg.Traffic.GRPC.RequestBytes()) != `{"job_id":"draincheck"}` || cfg.Traffic.GRPC.Metadata["authorization"] != "Bearer example" {
		t.Fatalf("gRPC traffic = %#v", cfg.Traffic.GRPC)
	}
	stream := cfg.Streaming.GRPC
	if !stream.Enabled || stream.Method != "example.jobs.v1.Worker/Watch" || stream.MinimumMessages != 2 || stream.ExpectedCode != "UNAVAILABLE" || stream.EstablishTimeout.Value() != 750*time.Millisecond || stream.CloseTimeout.Value() != 3*time.Second {
		t.Fatalf("gRPC stream = %#v", stream)
	}
}

func TestLoadFileResolvesGRPCFilesRelativeToConfig(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "request.json"), []byte(`{"service":"slow"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "service.protoset"), []byte("descriptor"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "draincheck.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
target:
  image: example:test
  container_port: 8080
traffic:
  driver: grpc
  grpc:
    method: grpc.health.v1.Health/Check
    request_file: request.json
    descriptor_set: service.protoset
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if string(cfg.Traffic.GRPC.RequestBytes()) != `{"service":"slow"}` || string(cfg.Traffic.GRPC.DescriptorBytes()) != "descriptor" {
		t.Fatalf("resolved gRPC files = request %q descriptor %q", cfg.Traffic.GRPC.RequestBytes(), cfg.Traffic.GRPC.DescriptorBytes())
	}
}

func TestValidateRejectsInvalidGRPCConfiguration(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "method", configure: func(cfg *Config) { cfg.Traffic.GRPC.Method = "bad" }, contains: "package.Service/Method"},
		{name: "request", configure: func(cfg *Config) { cfg.Traffic.GRPC.Request = "{" }, contains: "valid JSON"},
		{name: "metadata", configure: func(cfg *Config) { cfg.Traffic.GRPC.Metadata = map[string]string{"Authorization": "secret"} }, contains: "invalid key"},
		{name: "reserved metadata", configure: func(cfg *Config) { cfg.Traffic.GRPC.Metadata = map[string]string{"grpc-timeout": "1S"} }, contains: "invalid key"},
		{name: "code", configure: func(cfg *Config) { cfg.Traffic.GRPC.ExpectedCodes = []string{"SUCCESS"} }, contains: "unknown gRPC status"},
		{name: "descriptor", configure: func(cfg *Config) { cfg.Traffic.GRPC.DescriptorSet = "service.protoset" }, contains: "descriptor_set was not resolved"},
		{name: "stream close timeout", configure: func(cfg *Config) {
			cfg.Streaming.GRPC.Enabled = true
			cfg.Streaming.GRPC.Method = "example.Service/Watch"
			cfg.Streaming.GRPC.CloseTimeout = NewDuration(cfg.Shutdown.Deadline.Value() + time.Second)
		}, contains: "streaming.grpc.close_timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			cfg.Traffic.Driver = TrafficDriverGRPC
			cfg.Traffic.GRPC.Method = "example.Service/Run"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeRichHTTPRequest(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
traffic:
  request:
    method: POST
    path: /jobs
    headers:
      Content-Type: application/json
    body: '{"task":"drain"}'
    success_statuses: [201, 202]
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	request := cfg.Traffic.Request
	if request.Method != "POST" || string(request.BodyBytes()) != `{"task":"drain"}` || len(request.SuccessStatuses) != 2 || request.SuccessStatuses[0] != 201 || request.SuccessStatuses[1] != 202 {
		t.Fatalf("rich request = %#v", request)
	}
}

func TestLoadFileResolvesRequestBodyRelativeToConfig(t *testing.T) {
	directory := t.TempDir()
	bodyPath := filepath.Join(directory, "request.json")
	if err := os.WriteFile(bodyPath, []byte(`{"task":"from-file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "draincheck.yaml")
	if err := os.WriteFile(configPath, []byte(`
version: 1
target:
  image: example:test
  container_port: 8080
traffic:
  request:
    method: POST
    path: /jobs
    body_file: request.json
    success_statuses: [202]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if got := string(cfg.Traffic.Request.BodyBytes()); got != `{"task":"from-file"}` {
		t.Fatalf("resolved body = %q", got)
	}
	first := cfg.Traffic.Request.BodyBytes()
	first[0] = 'X'
	if got := string(cfg.Traffic.Request.BodyBytes()); got != `{"task":"from-file"}` {
		t.Fatalf("body was mutable through returned bytes: %q", got)
	}
}

func TestLoadFileRejectsInvalidRequestBodyFile(t *testing.T) {
	for _, test := range []struct {
		name      string
		bodyFile  string
		writeBody bool
		contains  string
	}{
		{name: "missing", bodyFile: "missing.json", contains: "read traffic request body file"},
		{name: "oversized", bodyFile: "oversized.json", writeBody: true, contains: "exceeds 1048576 bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if test.writeBody {
				if err := os.WriteFile(filepath.Join(directory, test.bodyFile), bytes.Repeat([]byte("x"), maxRequestBodyBytes+1), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			configPath := filepath.Join(directory, "draincheck.yaml")
			content := "version: 1\ntarget:\n  image: example:test\n  container_port: 8080\ntraffic:\n  request:\n    method: POST\n    path: /jobs\n    body_file: " + test.bodyFile + "\n"
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadFile(configPath)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("LoadFile error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestValidateRejectsInvalidRichHTTPRequest(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "body sources", configure: func(cfg *Config) {
			cfg.Traffic.Request.Body = "inline"
			cfg.Traffic.Request.BodyFile = "request.txt"
		}, contains: "mutually exclusive"},
		{name: "status range", configure: func(cfg *Config) {
			cfg.Traffic.Request.SuccessStatuses = []int{99, 600}
		}, contains: "between 100 and 599"},
		{name: "duplicate status", configure: func(cfg *Config) {
			cfg.Traffic.Request.SuccessStatuses = []int{202, 202}
		}, contains: "duplicate code 202"},
		{name: "blank body file", configure: func(cfg *Config) {
			cfg.Traffic.Request.BodyFile = " "
		}, contains: "body_file must not be blank"},
		{name: "unresolved body file", configure: func(cfg *Config) {
			cfg.Traffic.Request.BodyFile = "request.json"
		}, contains: "body_file was not resolved"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeSSEStreaming(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
streaming:
  sse:
    enabled: true
    path: /stream
    headers:
      Authorization: Bearer example
    initial_event: connected
    terminal_event: draining
    establish_timeout: 750ms
    close_timeout: 3s
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	sse := cfg.Streaming.SSE
	if !sse.Enabled || sse.Path != "/stream" || sse.Headers["Authorization"] != "Bearer example" || sse.InitialEvent != "connected" || sse.TerminalEvent != "draining" || sse.EstablishTimeout.Value() != 750*time.Millisecond || sse.CloseTimeout.Value() != 3*time.Second {
		t.Fatalf("SSE streaming configuration = %#v", sse)
	}
}

func TestValidateRejectsInvalidSSEStreaming(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "path", configure: func(cfg *Config) { cfg.Streaming.SSE.Path = "events" }, contains: "streaming.sse.path"},
		{name: "initial event", configure: func(cfg *Config) { cfg.Streaming.SSE.InitialEvent = "" }, contains: "streaming.sse.initial_event"},
		{name: "event newline", configure: func(cfg *Config) { cfg.Streaming.SSE.TerminalEvent = "done\nnow" }, contains: "streaming.sse.terminal_event"},
		{name: "establish timeout", configure: func(cfg *Config) { cfg.Streaming.SSE.EstablishTimeout = NewDuration(31 * time.Second) }, contains: "streaming.sse.establish_timeout"},
		{name: "close timeout", configure: func(cfg *Config) {
			cfg.Streaming.SSE.Enabled = true
			cfg.Streaming.SSE.CloseTimeout = NewDuration(cfg.Shutdown.Deadline.Value() + time.Second)
		}, contains: "streaming.sse.close_timeout"},
		{name: "header", configure: func(cfg *Config) { cfg.Streaming.SSE.Headers = map[string]string{"Bad\nHeader": "value"} }, contains: "streaming.sse.headers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeWebSocketStreaming(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
streaming:
  websocket:
    enabled: true
    path: /socket
    headers:
      Authorization: Bearer example
    subprotocols: [draincheck.v1]
    terminal_message: draining
    close_code: 1001
    establish_timeout: 750ms
    close_timeout: 3s
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	webSocket := cfg.Streaming.WebSocket
	if !webSocket.Enabled || webSocket.Path != "/socket" || webSocket.Headers["Authorization"] != "Bearer example" || len(webSocket.Subprotocols) != 1 || webSocket.Subprotocols[0] != "draincheck.v1" || webSocket.TerminalMessage != "draining" || webSocket.CloseCode != 1001 || webSocket.EstablishTimeout.Value() != 750*time.Millisecond || webSocket.CloseTimeout.Value() != 3*time.Second {
		t.Fatalf("WebSocket streaming configuration = %#v", webSocket)
	}
}

func TestValidateRejectsInvalidWebSocketStreaming(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "path", configure: func(cfg *Config) { cfg.Streaming.WebSocket.Path = "socket" }, contains: "streaming.websocket.path"},
		{name: "terminal message", configure: func(cfg *Config) { cfg.Streaming.WebSocket.TerminalMessage = strings.Repeat("x", 4097) }, contains: "streaming.websocket.terminal_message"},
		{name: "reserved close code", configure: func(cfg *Config) { cfg.Streaming.WebSocket.CloseCode = 1006 }, contains: "streaming.websocket.close_code"},
		{name: "unassigned close code", configure: func(cfg *Config) { cfg.Streaming.WebSocket.CloseCode = 2000 }, contains: "streaming.websocket.close_code"},
		{name: "close code range", configure: func(cfg *Config) { cfg.Streaming.WebSocket.CloseCode = 5000 }, contains: "streaming.websocket.close_code"},
		{name: "establish timeout", configure: func(cfg *Config) { cfg.Streaming.WebSocket.EstablishTimeout = NewDuration(31 * time.Second) }, contains: "streaming.websocket.establish_timeout"},
		{name: "close timeout", configure: func(cfg *Config) {
			cfg.Streaming.WebSocket.Enabled = true
			cfg.Streaming.WebSocket.CloseTimeout = NewDuration(cfg.Shutdown.Deadline.Value() + time.Second)
		}, contains: "streaming.websocket.close_timeout"},
		{name: "header", configure: func(cfg *Config) { cfg.Streaming.WebSocket.Headers = map[string]string{"Bad\nHeader": "value"} }, contains: "streaming.websocket.headers"},
		{name: "subprotocol", configure: func(cfg *Config) { cfg.Streaming.WebSocket.Subprotocols = []string{"not valid"} }, contains: "streaming.websocket.subprotocols"},
		{name: "duplicate subprotocol", configure: func(cfg *Config) { cfg.Streaming.WebSocket.Subprotocols = []string{"chat", "chat"} }, contains: "duplicate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeTraceTelemetry(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
telemetry:
  traces:
    enabled: true
    minimum_correlated_spans: 3
    flush_timeout: 750ms
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if !cfg.Telemetry.Traces.Enabled || cfg.Telemetry.Traces.MinimumCorrelatedSpans != 3 || cfg.Telemetry.Traces.FlushTimeout.Value() != 750*time.Millisecond {
		t.Fatalf("trace telemetry configuration = %#v", cfg.Telemetry.Traces)
	}
}

func TestValidateRejectsInvalidTraceTelemetry(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "minimum", configure: func(cfg *Config) { cfg.Telemetry.Traces.MinimumCorrelatedSpans = 0 }, contains: "telemetry.traces.minimum_correlated_spans"},
		{name: "zero timeout", configure: func(cfg *Config) { cfg.Telemetry.Traces.FlushTimeout = NewDuration(0) }, contains: "telemetry.traces.flush_timeout"},
		{name: "long timeout", configure: func(cfg *Config) { cfg.Telemetry.Traces.FlushTimeout = NewDuration(31 * time.Second) }, contains: "telemetry.traces.flush_timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeMetricTelemetry(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
telemetry:
  metrics:
    enabled: true
    minimum_data_points: 3
    flush_timeout: 750ms
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if !cfg.Telemetry.Metrics.Enabled || cfg.Telemetry.Metrics.MinimumDataPoints != 3 || cfg.Telemetry.Metrics.FlushTimeout.Value() != 750*time.Millisecond {
		t.Fatalf("metric telemetry configuration = %#v", cfg.Telemetry.Metrics)
	}
}

func TestValidateRejectsInvalidMetricTelemetry(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "minimum", configure: func(cfg *Config) { cfg.Telemetry.Metrics.MinimumDataPoints = 0 }, contains: "telemetry.metrics.minimum_data_points"},
		{name: "zero timeout", configure: func(cfg *Config) { cfg.Telemetry.Metrics.FlushTimeout = NewDuration(0) }, contains: "telemetry.metrics.flush_timeout"},
		{name: "long timeout", configure: func(cfg *Config) { cfg.Telemetry.Metrics.FlushTimeout = NewDuration(31 * time.Second) }, contains: "telemetry.metrics.flush_timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeRepeatBudgets(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
repeat:
  budgets:
    startup_ready_p95: 2s
    readiness_withdrawal_p95: 750ms
    container_exit_p95: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	budgets := cfg.Repeat.Budgets
	if budgets.StartupReadyP95.Value() != 2*time.Second || budgets.ReadinessWithdrawalP95.Value() != 750*time.Millisecond || budgets.ContainerExitP95.Value() != 5*time.Second {
		t.Fatalf("repeat budgets = %#v", budgets)
	}
}

func TestValidateRejectsSubMillisecondRepeatBudgets(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "startup", configure: func(cfg *Config) { cfg.Repeat.Budgets.StartupReadyP95 = NewDuration(0) }, contains: "repeat.budgets.startup_ready_p95"},
		{name: "withdrawal", configure: func(cfg *Config) { cfg.Repeat.Budgets.ReadinessWithdrawalP95 = NewDuration(time.Microsecond) }, contains: "repeat.budgets.readiness_withdrawal_p95"},
		{name: "exit", configure: func(cfg *Config) { cfg.Repeat.Budgets.ContainerExitP95 = NewDuration(time.Nanosecond) }, contains: "repeat.budgets.container_exit_p95"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodePostSignalPolicy(t *testing.T) {
	cfg, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
traffic:
  post_signal:
    policy: reject
    delay: 250ms
    count: 3
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Validate(true); err != nil {
		t.Fatal(err)
	}
	if cfg.Traffic.PostSignal.Policy != PostSignalReject || cfg.Traffic.PostSignal.Delay.Value() != 250*time.Millisecond || cfg.Traffic.PostSignal.Count != 3 {
		t.Fatalf("post-signal configuration = %#v", cfg.Traffic.PostSignal)
	}
}

func TestValidateRejectsInvalidPostSignalPolicy(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Config)
		contains  string
	}{
		{name: "policy", configure: func(cfg *Config) { cfg.Traffic.PostSignal.Policy = "sometimes" }, contains: "traffic.post_signal.policy"},
		{name: "count", configure: func(cfg *Config) { cfg.Traffic.PostSignal.Count = 0 }, contains: "traffic.post_signal.count"},
		{name: "delay", configure: func(cfg *Config) { cfg.Traffic.PostSignal.Delay = cfg.Shutdown.Deadline }, contains: "traffic.post_signal.delay"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			cfg.Target.Image = "example:test"
			test.configure(&cfg)
			err := cfg.Validate(true)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validation error = %v, want %q", err, test.contains)
			}
		})
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	_, err := Decode(strings.NewReader(`
version: 1
target:
  image: example:test
  container_port: 8080
  container_por: 9090
`))
	if err == nil || !strings.Contains(err.Error(), "container_por") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestDecodeRejectsDuplicateKeys(t *testing.T) {
	_, err := Decode(strings.NewReader(`
version: 1
target:
  image: first:test
  image: second:test
  container_port: 8080
`))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "already defined") {
		t.Fatalf("expected duplicate-key error, got %v", err)
	}
}

func TestDecodeRejectsMultipleDocuments(t *testing.T) {
	_, err := Decode(strings.NewReader("version: 1\n---\nversion: 1\n"))
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("expected multi-document error, got %v", err)
	}
}

func TestValidateReportsAllCoreProblems(t *testing.T) {
	cfg := Default()
	cfg.Version = 99
	cfg.Target.ContainerPort = 0
	cfg.Readiness.Path = "ready"
	cfg.Traffic.Concurrency = cfg.Traffic.Count + 1
	err := cfg.Validate(true)
	if err == nil {
		t.Fatal("expected validation error")
	}
	for _, expected := range []string{"version", "target.image", "container_port", "readiness.path", "traffic.concurrency"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("error %q does not contain %q", err, expected)
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte("version: 1\ntarget:\n  image: x\n  container_port: 8080\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(strings.NewReader(string(data)))
	})
}
