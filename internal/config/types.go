package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const CurrentVersion = 1

const (
	PostSignalDisabled   = "disabled"
	PostSignalAccept     = "accept"
	PostSignalReject     = "reject"
	ReadinessDriverHTTP  = "http"
	ReadinessDriverGRPC  = "grpc"
	ReadinessDriverExec  = "exec"
	TrafficDriverHTTP    = "http"
	TrafficDriverCommand = "command"
	TrafficDriverGRPC    = "grpc"
)

var signalPattern = regexp.MustCompile(`^SIG[A-Z0-9]+$`)

type Config struct {
	Version    int        `json:"version" yaml:"version" jsonschema:"required"`
	Target     Target     `json:"target" yaml:"target" jsonschema:"required"`
	Readiness  Readiness  `json:"readiness" yaml:"readiness" jsonschema:"required"`
	Traffic    Traffic    `json:"traffic" yaml:"traffic" jsonschema:"required"`
	Streaming  Streaming  `json:"streaming" yaml:"streaming"`
	Telemetry  Telemetry  `json:"telemetry" yaml:"telemetry"`
	Repeat     Repeat     `json:"repeat" yaml:"repeat"`
	Shutdown   Shutdown   `json:"shutdown" yaml:"shutdown" jsonschema:"required"`
	Assertions Assertions `json:"assertions" yaml:"assertions" jsonschema:"required"`
}

type Target struct {
	Image         string            `json:"image" yaml:"image"`
	ContainerPort int               `json:"container_port" yaml:"container_port" jsonschema:"required,minimum=1,maximum=65535"`
	Environment   map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
}

type Readiness struct {
	Driver         string        `json:"driver" yaml:"driver" jsonschema:"enum=http,enum=grpc,enum=exec"`
	ContainerPort  *int          `json:"container_port,omitempty" yaml:"container_port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Path           string        `json:"path" yaml:"path" jsonschema:"pattern=^/"`
	SuccessStatus  int           `json:"success_status" yaml:"success_status" jsonschema:"minimum=100,maximum=599"`
	GRPC           GRPCReadiness `json:"grpc" yaml:"grpc"`
	Exec           ExecReadiness `json:"exec" yaml:"exec"`
	StartupTimeout Duration      `json:"startup_timeout" yaml:"startup_timeout" jsonschema:"required,pattern=^[0-9]"`
	Interval       Duration      `json:"interval" yaml:"interval" jsonschema:"required,pattern=^[0-9]"`
}

type GRPCReadiness struct {
	Service string `json:"service,omitempty" yaml:"service,omitempty" jsonschema:"maxLength=1024"`
}

type ExecReadiness struct {
	Command []string `json:"command,omitempty" yaml:"command,omitempty" jsonschema:"minItems=1,maxItems=64"`
}

type Traffic struct {
	ContainerPort  *int              `json:"container_port,omitempty" yaml:"container_port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Driver         string            `json:"driver" yaml:"driver" jsonschema:"enum=http,enum=command,enum=grpc"`
	Request        Request           `json:"request" yaml:"request"`
	Command        CommandTraffic    `json:"command" yaml:"command"`
	GRPC           GRPCTraffic       `json:"grpc" yaml:"grpc"`
	Count          int               `json:"count" yaml:"count" jsonschema:"required,minimum=1"`
	Concurrency    int               `json:"concurrency" yaml:"concurrency" jsonschema:"required,minimum=1"`
	ShutdownAfter  Duration          `json:"shutdown_after" yaml:"shutdown_after" jsonschema:"required,pattern=^[0-9]"`
	RequestTimeout Duration          `json:"request_timeout" yaml:"request_timeout" jsonschema:"required,pattern=^[0-9]"`
	PostSignal     PostSignalTraffic `json:"post_signal" yaml:"post_signal"`
}

type CommandTraffic struct {
	Executable         string            `json:"executable,omitempty" yaml:"executable,omitempty"`
	Args               []string          `json:"args,omitempty" yaml:"args,omitempty" jsonschema:"maxItems=64"`
	Environment        map[string]string `json:"environment,omitempty" yaml:"environment,omitempty"`
	WorkingDirectory   string            `json:"working_directory,omitempty" yaml:"working_directory,omitempty"`
	resolvedExecutable string
	resolvedDirectory  string
	resolved           bool
}

func (c CommandTraffic) ResolvedExecutable() string { return c.resolvedExecutable }
func (c CommandTraffic) ResolvedDirectory() string  { return c.resolvedDirectory }

type GRPCTraffic struct {
	Method          string            `json:"method" yaml:"method"`
	Request         string            `json:"request,omitempty" yaml:"request,omitempty" jsonschema:"maxLength=1048576"`
	RequestFile     string            `json:"request_file,omitempty" yaml:"request_file,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	DescriptorSet   string            `json:"descriptor_set,omitempty" yaml:"descriptor_set,omitempty"`
	ExpectedCodes   []string          `json:"expected_codes,omitempty" yaml:"expected_codes,omitempty" jsonschema:"maxItems=17"`
	requestBytes    []byte
	requestResolved bool
	descriptorBytes []byte
	descriptorReady bool
}

func (g GRPCTraffic) RequestBytes() []byte {
	if g.requestBytes != nil {
		return append([]byte(nil), g.requestBytes...)
	}
	if g.Request == "" {
		return []byte("{}")
	}
	return []byte(g.Request)
}

func (g GRPCTraffic) DescriptorBytes() []byte {
	return append([]byte(nil), g.descriptorBytes...)
}

type PostSignalTraffic struct {
	Policy string   `json:"policy" yaml:"policy" jsonschema:"required,enum=disabled,enum=accept,enum=reject"`
	Delay  Duration `json:"delay" yaml:"delay" jsonschema:"required,pattern=^[0-9]"`
	Count  int      `json:"count" yaml:"count" jsonschema:"required,minimum=1,maximum=100"`
}

type Request struct {
	Method          string            `json:"method" yaml:"method" jsonschema:"required"`
	Path            string            `json:"path" yaml:"path" jsonschema:"required,pattern=^/"`
	Headers         map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body            string            `json:"body,omitempty" yaml:"body,omitempty" jsonschema:"maxLength=1048576"`
	BodyFile        string            `json:"body_file,omitempty" yaml:"body_file,omitempty"`
	SuccessStatuses []int             `json:"success_statuses,omitempty" yaml:"success_statuses,omitempty" jsonschema:"maxItems=100"`
	bodyBytes       []byte
	bodyResolved    bool
}

// BodyBytes returns an isolated copy of the resolved request body.
func (r Request) BodyBytes() []byte {
	if r.bodyBytes != nil {
		return append([]byte(nil), r.bodyBytes...)
	}
	return []byte(r.Body)
}

type Streaming struct {
	SSE       SSEStreaming       `json:"sse" yaml:"sse"`
	WebSocket WebSocketStreaming `json:"websocket" yaml:"websocket"`
	GRPC      GRPCStreaming      `json:"grpc" yaml:"grpc"`
}

type SSEStreaming struct {
	Enabled          bool              `json:"enabled" yaml:"enabled"`
	ContainerPort    *int              `json:"container_port,omitempty" yaml:"container_port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Path             string            `json:"path" yaml:"path" jsonschema:"required,pattern=^/"`
	Headers          map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	InitialEvent     string            `json:"initial_event" yaml:"initial_event" jsonschema:"required"`
	TerminalEvent    string            `json:"terminal_event" yaml:"terminal_event"`
	EstablishTimeout Duration          `json:"establish_timeout" yaml:"establish_timeout" jsonschema:"required,pattern=^[0-9]"`
	CloseTimeout     Duration          `json:"close_timeout" yaml:"close_timeout" jsonschema:"required,pattern=^[0-9]"`
}

type WebSocketStreaming struct {
	Enabled          bool              `json:"enabled" yaml:"enabled"`
	ContainerPort    *int              `json:"container_port,omitempty" yaml:"container_port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Path             string            `json:"path" yaml:"path" jsonschema:"required,pattern=^/"`
	Headers          map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Subprotocols     []string          `json:"subprotocols,omitempty" yaml:"subprotocols,omitempty" jsonschema:"maxItems=16"`
	TerminalMessage  string            `json:"terminal_message" yaml:"terminal_message" jsonschema:"maxLength=4096"`
	CloseCode        int               `json:"close_code" yaml:"close_code" jsonschema:"required,minimum=1000,maximum=4999"`
	EstablishTimeout Duration          `json:"establish_timeout" yaml:"establish_timeout" jsonschema:"required,pattern=^[0-9]"`
	CloseTimeout     Duration          `json:"close_timeout" yaml:"close_timeout" jsonschema:"required,pattern=^[0-9]"`
}

type GRPCStreaming struct {
	Enabled          bool              `json:"enabled" yaml:"enabled"`
	ContainerPort    *int              `json:"container_port,omitempty" yaml:"container_port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Method           string            `json:"method" yaml:"method"`
	Request          string            `json:"request,omitempty" yaml:"request,omitempty" jsonschema:"maxLength=1048576"`
	RequestFile      string            `json:"request_file,omitempty" yaml:"request_file,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	DescriptorSet    string            `json:"descriptor_set,omitempty" yaml:"descriptor_set,omitempty"`
	MinimumMessages  int               `json:"minimum_messages" yaml:"minimum_messages" jsonschema:"required,minimum=1,maximum=10000"`
	ExpectedCode     string            `json:"expected_code" yaml:"expected_code" jsonschema:"required"`
	EstablishTimeout Duration          `json:"establish_timeout" yaml:"establish_timeout" jsonschema:"required,pattern=^[0-9]"`
	CloseTimeout     Duration          `json:"close_timeout" yaml:"close_timeout" jsonschema:"required,pattern=^[0-9]"`
	requestBytes     []byte
	requestResolved  bool
	descriptorBytes  []byte
	descriptorReady  bool
}

func (g GRPCStreaming) RequestBytes() []byte {
	if g.requestBytes != nil {
		return append([]byte(nil), g.requestBytes...)
	}
	if g.Request == "" {
		return []byte("{}")
	}
	return []byte(g.Request)
}

func (g GRPCStreaming) DescriptorBytes() []byte {
	return append([]byte(nil), g.descriptorBytes...)
}

type Telemetry struct {
	Traces  TraceTelemetry  `json:"traces" yaml:"traces"`
	Metrics MetricTelemetry `json:"metrics" yaml:"metrics"`
}

type TraceTelemetry struct {
	Enabled                bool     `json:"enabled" yaml:"enabled"`
	MinimumCorrelatedSpans int      `json:"minimum_correlated_spans" yaml:"minimum_correlated_spans" jsonschema:"minimum=1,maximum=100"`
	FlushTimeout           Duration `json:"flush_timeout" yaml:"flush_timeout" jsonschema:"pattern=^[0-9]"`
}

type MetricTelemetry struct {
	Enabled           bool     `json:"enabled" yaml:"enabled"`
	MinimumDataPoints int      `json:"minimum_data_points" yaml:"minimum_data_points" jsonschema:"minimum=1,maximum=10000"`
	FlushTimeout      Duration `json:"flush_timeout" yaml:"flush_timeout" jsonschema:"pattern=^[0-9]"`
}

type Repeat struct {
	Budgets RepeatBudgets `json:"budgets" yaml:"budgets"`
}

type RepeatBudgets struct {
	StartupReadyP95        Duration `json:"startup_ready_p95,omitempty" yaml:"startup_ready_p95,omitempty" jsonschema:"pattern=^[0-9]"`
	ReadinessWithdrawalP95 Duration `json:"readiness_withdrawal_p95,omitempty" yaml:"readiness_withdrawal_p95,omitempty" jsonschema:"pattern=^[0-9]"`
	ContainerExitP95       Duration `json:"container_exit_p95,omitempty" yaml:"container_exit_p95,omitempty" jsonschema:"pattern=^[0-9]"`
}

type Shutdown struct {
	Signal   string       `json:"signal" yaml:"signal" jsonschema:"required,pattern=^SIG[A-Z0-9]+$"`
	Deadline Duration     `json:"deadline" yaml:"deadline" jsonschema:"required,pattern=^[0-9]"`
	PreStop  *PreStopHook `json:"pre_stop,omitempty" yaml:"pre_stop,omitempty"`
}

type PreStopHook struct {
	Exec ExecHook `json:"exec" yaml:"exec" jsonschema:"required"`
}

type ExecHook struct {
	Command []string `json:"command" yaml:"command" jsonschema:"required,minItems=1,maxItems=64"`
}

type Assertions struct {
	ReadinessWithdrawnWithin Duration `json:"readiness_withdrawn_within" yaml:"readiness_withdrawn_within" jsonschema:"required,pattern=^[0-9]"`
	InflightRequestsComplete bool     `json:"inflight_requests_complete" yaml:"inflight_requests_complete"`
	MaxFailedRequests        int      `json:"max_failed_requests" yaml:"max_failed_requests" jsonschema:"minimum=0"`
	ExitCode                 int      `json:"exit_code" yaml:"exit_code"`
	ForbidForceKill          bool     `json:"forbid_force_kill" yaml:"forbid_force_kill"`
}

func Default() Config {
	return Config{
		Version: CurrentVersion,
		Target: Target{
			ContainerPort: 8080,
		},
		Readiness: Readiness{
			Driver:         ReadinessDriverHTTP,
			Path:           "/ready",
			SuccessStatus:  http.StatusOK,
			StartupTimeout: NewDuration(20 * time.Second),
			Interval:       NewDuration(200 * time.Millisecond),
		},
		Traffic: Traffic{
			Driver: TrafficDriverHTTP,
			Request: Request{
				Method: http.MethodGet,
				Path:   "/work?delay=2s",
			},
			GRPC: GRPCTraffic{
				ExpectedCodes: []string{"OK"},
			},
			Count:          5,
			Concurrency:    5,
			ShutdownAfter:  NewDuration(500 * time.Millisecond),
			RequestTimeout: NewDuration(10 * time.Second),
			PostSignal: PostSignalTraffic{
				Policy: PostSignalDisabled,
				Delay:  NewDuration(0),
				Count:  1,
			},
		},
		Streaming: Streaming{
			SSE: SSEStreaming{
				Enabled:          false,
				Path:             "/events",
				InitialEvent:     "ready",
				TerminalEvent:    "shutdown",
				EstablishTimeout: NewDuration(2 * time.Second),
				CloseTimeout:     NewDuration(5 * time.Second),
			},
			WebSocket: WebSocketStreaming{
				Enabled:          false,
				Path:             "/ws",
				TerminalMessage:  "shutdown",
				CloseCode:        1000,
				EstablishTimeout: NewDuration(2 * time.Second),
				CloseTimeout:     NewDuration(5 * time.Second),
			},
			GRPC: GRPCStreaming{
				Enabled:          false,
				MinimumMessages:  1,
				ExpectedCode:     "OK",
				EstablishTimeout: NewDuration(2 * time.Second),
				CloseTimeout:     NewDuration(5 * time.Second),
			},
		},
		Telemetry: Telemetry{
			Traces: TraceTelemetry{
				Enabled:                false,
				MinimumCorrelatedSpans: 1,
				FlushTimeout:           NewDuration(2 * time.Second),
			},
			Metrics: MetricTelemetry{
				Enabled:           false,
				MinimumDataPoints: 1,
				FlushTimeout:      NewDuration(2 * time.Second),
			},
		},
		Shutdown: Shutdown{
			Signal:   "SIGTERM",
			Deadline: NewDuration(15 * time.Second),
		},
		Assertions: Assertions{
			ReadinessWithdrawnWithin: NewDuration(2 * time.Second),
			InflightRequestsComplete: true,
			MaxFailedRequests:        0,
			ExitCode:                 0,
			ForbidForceKill:          true,
		},
	}
}

func (c Config) Validate(requireImage bool) error {
	var problems []string

	if c.Version != CurrentVersion {
		problems = append(problems, fmt.Sprintf("version must be %d", CurrentVersion))
	}
	if requireImage && strings.TrimSpace(c.Target.Image) == "" {
		problems = append(problems, "target.image is required when no image override is supplied")
	}
	if c.Target.ContainerPort < 1 || c.Target.ContainerPort > 65535 {
		problems = append(problems, "target.container_port must be between 1 and 65535")
	}
	optionalPort(&problems, "readiness.container_port", c.Readiness.ContainerPort)
	optionalPort(&problems, "traffic.container_port", c.Traffic.ContainerPort)
	optionalPort(&problems, "streaming.sse.container_port", c.Streaming.SSE.ContainerPort)
	optionalPort(&problems, "streaming.websocket.container_port", c.Streaming.WebSocket.ContainerPort)
	optionalPort(&problems, "streaming.grpc.container_port", c.Streaming.GRPC.ContainerPort)
	for key := range c.Target.Environment {
		if strings.TrimSpace(key) == "" || strings.Contains(key, "=") {
			problems = append(problems, fmt.Sprintf("target.environment contains invalid key %q", key))
		}
	}
	switch c.Readiness.Driver {
	case ReadinessDriverHTTP:
		if !strings.HasPrefix(c.Readiness.Path, "/") {
			problems = append(problems, "readiness.path must begin with /")
		}
		if c.Readiness.SuccessStatus < 100 || c.Readiness.SuccessStatus > 599 {
			problems = append(problems, "readiness.success_status must be between 100 and 599")
		}
	case ReadinessDriverGRPC:
		service := c.Readiness.GRPC.Service
		if len(service) > 1024 || service != strings.TrimSpace(service) || strings.ContainsAny(service, "\x00\r\n\t") {
			problems = append(problems, "readiness.grpc.service must be at most 1024 bytes with no surrounding whitespace or control characters")
		}
	case ReadinessDriverExec:
		if c.Readiness.ContainerPort != nil {
			problems = append(problems, "readiness.container_port is not valid when readiness.driver is exec")
		}
		if len(c.Readiness.Exec.Command) == 0 {
			problems = append(problems, "readiness.exec.command is required when readiness.driver is exec")
		} else if len(c.Readiness.Exec.Command) > 64 {
			problems = append(problems, "readiness.exec.command must contain at most 64 arguments")
		}
		for index, argument := range c.Readiness.Exec.Command {
			if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
				problems = append(problems, fmt.Sprintf("readiness.exec.command[%d] must be at most 4096 bytes and contain no NUL byte", index))
			}
		}
		if len(c.Readiness.Exec.Command) > 0 && strings.TrimSpace(c.Readiness.Exec.Command[0]) == "" {
			problems = append(problems, "readiness.exec.command[0] must name an executable")
		}
	default:
		problems = append(problems, "readiness.driver must be http, grpc, or exec")
	}
	if c.Readiness.Driver != ReadinessDriverExec && len(c.Readiness.Exec.Command) > 0 {
		problems = append(problems, "readiness.exec is only valid when readiness.driver is exec")
	}
	positiveDuration(&problems, "readiness.startup_timeout", c.Readiness.StartupTimeout)
	positiveDuration(&problems, "readiness.interval", c.Readiness.Interval)
	if c.Readiness.Interval.Value() > c.Readiness.StartupTimeout.Value() {
		problems = append(problems, "readiness.interval must not exceed readiness.startup_timeout")
	}

	c.Traffic.Request.Method = strings.ToUpper(strings.TrimSpace(c.Traffic.Request.Method))
	switch c.Traffic.Driver {
	case TrafficDriverHTTP:
		if strings.TrimSpace(c.Traffic.Command.Executable) != "" || len(c.Traffic.Command.Args) > 0 || len(c.Traffic.Command.Environment) > 0 || strings.TrimSpace(c.Traffic.Command.WorkingDirectory) != "" {
			problems = append(problems, "traffic.command is only valid when traffic.driver is command")
		}
	case TrafficDriverCommand:
		if strings.TrimSpace(c.Traffic.Command.Executable) == "" {
			problems = append(problems, "traffic.command.executable is required when traffic.driver is command")
		}
		if !c.Traffic.Command.resolved {
			problems = append(problems, "traffic.command was not resolved; load command configurations with config.LoadFile")
		}
		if c.Telemetry.Traces.Enabled {
			problems = append(problems, "telemetry.traces is not supported with traffic.driver command because trace correlation requires HTTP header injection")
		}
	case TrafficDriverGRPC:
		validateGRPCCall(&problems, "traffic.grpc", c.Traffic.GRPC.Method, c.Traffic.GRPC.Request, c.Traffic.GRPC.RequestFile, c.Traffic.GRPC.requestResolved, c.Traffic.GRPC.Metadata, c.Traffic.GRPC.DescriptorSet, c.Traffic.GRPC.descriptorReady)
		validateGRPCCodes(&problems, "traffic.grpc.expected_codes", c.Traffic.GRPC.ExpectedCodes)
	default:
		problems = append(problems, "traffic.driver must be http, command, or grpc")
	}
	if len(c.Traffic.Command.Args) > 64 {
		problems = append(problems, "traffic.command.args must contain at most 64 values")
	}
	for index, arg := range c.Traffic.Command.Args {
		if len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
			problems = append(problems, fmt.Sprintf("traffic.command.args[%d] must be at most 4096 bytes and contain no NUL", index))
		}
	}
	if len(c.Traffic.Command.Environment) > 64 {
		problems = append(problems, "traffic.command.environment must contain at most 64 values")
	}
	for key, value := range c.Traffic.Command.Environment {
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if normalized == "" || strings.ContainsAny(key, "=\x00\r\n") {
			problems = append(problems, fmt.Sprintf("traffic.command.environment contains invalid key %q", key))
		}
		if strings.HasPrefix(normalized, "DRAINCHECK_") {
			problems = append(problems, fmt.Sprintf("traffic.command.environment key %q uses the reserved DRAINCHECK_ prefix", key))
		}
		if strings.ContainsRune(value, '\x00') {
			problems = append(problems, fmt.Sprintf("traffic.command.environment contains a NUL value for %q", key))
		}
	}
	if c.Traffic.Request.Method == "" {
		problems = append(problems, "traffic.request.method is required")
	}
	if !strings.HasPrefix(c.Traffic.Request.Path, "/") {
		problems = append(problems, "traffic.request.path must begin with /")
	}
	if c.Traffic.Request.Body != "" && strings.TrimSpace(c.Traffic.Request.BodyFile) != "" {
		problems = append(problems, "traffic.request.body and traffic.request.body_file are mutually exclusive")
	}
	if c.Traffic.Request.BodyFile != "" && strings.TrimSpace(c.Traffic.Request.BodyFile) == "" {
		problems = append(problems, "traffic.request.body_file must not be blank")
	}
	if strings.TrimSpace(c.Traffic.Request.BodyFile) != "" && c.Traffic.Request.Body == "" && !c.Traffic.Request.bodyResolved {
		problems = append(problems, "traffic.request.body_file was not resolved; load file-backed configurations with config.LoadFile")
	}
	if len(c.Traffic.Request.Body) > maxRequestBodyBytes {
		problems = append(problems, fmt.Sprintf("traffic.request.body must not exceed %d bytes", maxRequestBodyBytes))
	}
	if len(c.Traffic.Request.SuccessStatuses) > 100 {
		problems = append(problems, "traffic.request.success_statuses must contain at most 100 codes")
	}
	seenStatuses := make(map[int]struct{}, len(c.Traffic.Request.SuccessStatuses))
	for _, status := range c.Traffic.Request.SuccessStatuses {
		if status < 100 || status > 599 {
			problems = append(problems, "traffic.request.success_statuses must contain codes between 100 and 599")
			continue
		}
		if _, found := seenStatuses[status]; found {
			problems = append(problems, fmt.Sprintf("traffic.request.success_statuses contains duplicate code %d", status))
		}
		seenStatuses[status] = struct{}{}
	}
	if c.Traffic.Count < 1 {
		problems = append(problems, "traffic.count must be at least 1")
	}
	if c.Traffic.Concurrency < 1 || c.Traffic.Concurrency > c.Traffic.Count {
		problems = append(problems, "traffic.concurrency must be between 1 and traffic.count")
	}
	nonNegativeDuration(&problems, "traffic.shutdown_after", c.Traffic.ShutdownAfter)
	positiveDuration(&problems, "traffic.request_timeout", c.Traffic.RequestTimeout)
	postSignalPolicy := c.Traffic.PostSignal.Policy
	switch postSignalPolicy {
	case PostSignalDisabled, PostSignalAccept, PostSignalReject:
	default:
		problems = append(problems, "traffic.post_signal.policy must be disabled, accept, or reject")
	}
	nonNegativeDuration(&problems, "traffic.post_signal.delay", c.Traffic.PostSignal.Delay)
	if c.Traffic.PostSignal.Count < 1 || c.Traffic.PostSignal.Count > 100 {
		problems = append(problems, "traffic.post_signal.count must be between 1 and 100")
	}
	for key := range c.Traffic.Request.Headers {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
			problems = append(problems, fmt.Sprintf("traffic.request.headers contains invalid key %q", key))
		}
	}
	if !strings.HasPrefix(c.Streaming.SSE.Path, "/") {
		problems = append(problems, "streaming.sse.path must begin with /")
	}
	if strings.TrimSpace(c.Streaming.SSE.InitialEvent) == "" || strings.ContainsAny(c.Streaming.SSE.InitialEvent, "\r\n") {
		problems = append(problems, "streaming.sse.initial_event must be a non-empty event name without newlines")
	}
	if strings.ContainsAny(c.Streaming.SSE.TerminalEvent, "\r\n") {
		problems = append(problems, "streaming.sse.terminal_event must not contain newlines")
	}
	positiveDuration(&problems, "streaming.sse.establish_timeout", c.Streaming.SSE.EstablishTimeout)
	if c.Streaming.SSE.EstablishTimeout.Value() > 30*time.Second {
		problems = append(problems, "streaming.sse.establish_timeout must not exceed 30s")
	}
	positiveDuration(&problems, "streaming.sse.close_timeout", c.Streaming.SSE.CloseTimeout)
	if c.Streaming.SSE.Enabled && c.Streaming.SSE.CloseTimeout.Value() > c.Shutdown.Deadline.Value() {
		problems = append(problems, "streaming.sse.close_timeout must not exceed shutdown.deadline")
	}
	for key, value := range c.Streaming.SSE.Headers {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
			problems = append(problems, fmt.Sprintf("streaming.sse.headers contains invalid key %q", key))
		}
		if strings.ContainsAny(value, "\r\n") {
			problems = append(problems, fmt.Sprintf("streaming.sse.headers contains invalid value for %q", key))
		}
	}
	if !strings.HasPrefix(c.Streaming.WebSocket.Path, "/") {
		problems = append(problems, "streaming.websocket.path must begin with /")
	}
	if len(c.Streaming.WebSocket.TerminalMessage) > 4096 {
		problems = append(problems, "streaming.websocket.terminal_message must not exceed 4096 bytes")
	}
	if !validWebSocketCloseCode(c.Streaming.WebSocket.CloseCode) {
		problems = append(problems, "streaming.websocket.close_code must be a sendable WebSocket status between 1000 and 4999")
	}
	positiveDuration(&problems, "streaming.websocket.establish_timeout", c.Streaming.WebSocket.EstablishTimeout)
	if c.Streaming.WebSocket.EstablishTimeout.Value() > 30*time.Second {
		problems = append(problems, "streaming.websocket.establish_timeout must not exceed 30s")
	}
	positiveDuration(&problems, "streaming.websocket.close_timeout", c.Streaming.WebSocket.CloseTimeout)
	if c.Streaming.WebSocket.Enabled && c.Streaming.WebSocket.CloseTimeout.Value() > c.Shutdown.Deadline.Value() {
		problems = append(problems, "streaming.websocket.close_timeout must not exceed shutdown.deadline")
	}
	for key, value := range c.Streaming.WebSocket.Headers {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "\r\n") {
			problems = append(problems, fmt.Sprintf("streaming.websocket.headers contains invalid key %q", key))
		}
		if strings.ContainsAny(value, "\r\n") {
			problems = append(problems, fmt.Sprintf("streaming.websocket.headers contains invalid value for %q", key))
		}
	}
	if len(c.Streaming.WebSocket.Subprotocols) > 16 {
		problems = append(problems, "streaming.websocket.subprotocols must contain at most 16 values")
	}
	seenSubprotocols := make(map[string]struct{}, len(c.Streaming.WebSocket.Subprotocols))
	for _, subprotocol := range c.Streaming.WebSocket.Subprotocols {
		if !validWebSocketSubprotocol(subprotocol) {
			problems = append(problems, fmt.Sprintf("streaming.websocket.subprotocols contains invalid value %q", subprotocol))
			continue
		}
		if _, found := seenSubprotocols[subprotocol]; found {
			problems = append(problems, fmt.Sprintf("streaming.websocket.subprotocols contains duplicate value %q", subprotocol))
		}
		seenSubprotocols[subprotocol] = struct{}{}
	}
	if c.Streaming.GRPC.Enabled {
		validateGRPCCall(&problems, "streaming.grpc", c.Streaming.GRPC.Method, c.Streaming.GRPC.Request, c.Streaming.GRPC.RequestFile, c.Streaming.GRPC.requestResolved, c.Streaming.GRPC.Metadata, c.Streaming.GRPC.DescriptorSet, c.Streaming.GRPC.descriptorReady)
	}
	if c.Streaming.GRPC.MinimumMessages < 1 || c.Streaming.GRPC.MinimumMessages > 10000 {
		problems = append(problems, "streaming.grpc.minimum_messages must be between 1 and 10000")
	}
	validateGRPCCodes(&problems, "streaming.grpc.expected_code", []string{c.Streaming.GRPC.ExpectedCode})
	positiveDuration(&problems, "streaming.grpc.establish_timeout", c.Streaming.GRPC.EstablishTimeout)
	if c.Streaming.GRPC.EstablishTimeout.Value() > 30*time.Second {
		problems = append(problems, "streaming.grpc.establish_timeout must not exceed 30s")
	}
	positiveDuration(&problems, "streaming.grpc.close_timeout", c.Streaming.GRPC.CloseTimeout)
	if c.Streaming.GRPC.Enabled && c.Streaming.GRPC.CloseTimeout.Value() > c.Shutdown.Deadline.Value() {
		problems = append(problems, "streaming.grpc.close_timeout must not exceed shutdown.deadline")
	}
	if c.Telemetry.Traces.MinimumCorrelatedSpans < 1 || c.Telemetry.Traces.MinimumCorrelatedSpans > 100 {
		problems = append(problems, "telemetry.traces.minimum_correlated_spans must be between 1 and 100")
	}
	positiveDuration(&problems, "telemetry.traces.flush_timeout", c.Telemetry.Traces.FlushTimeout)
	if c.Telemetry.Traces.FlushTimeout.Value() > 30*time.Second {
		problems = append(problems, "telemetry.traces.flush_timeout must not exceed 30s")
	}
	if c.Telemetry.Metrics.MinimumDataPoints < 1 || c.Telemetry.Metrics.MinimumDataPoints > 10000 {
		problems = append(problems, "telemetry.metrics.minimum_data_points must be between 1 and 10000")
	}
	positiveDuration(&problems, "telemetry.metrics.flush_timeout", c.Telemetry.Metrics.FlushTimeout)
	if c.Telemetry.Metrics.FlushTimeout.Value() > 30*time.Second {
		problems = append(problems, "telemetry.metrics.flush_timeout must not exceed 30s")
	}
	optionalBudgetDuration(&problems, "repeat.budgets.startup_ready_p95", c.Repeat.Budgets.StartupReadyP95)
	optionalBudgetDuration(&problems, "repeat.budgets.readiness_withdrawal_p95", c.Repeat.Budgets.ReadinessWithdrawalP95)
	optionalBudgetDuration(&problems, "repeat.budgets.container_exit_p95", c.Repeat.Budgets.ContainerExitP95)

	if !signalPattern.MatchString(c.Shutdown.Signal) {
		problems = append(problems, "shutdown.signal must look like SIGTERM")
	}
	positiveDuration(&problems, "shutdown.deadline", c.Shutdown.Deadline)
	if c.Shutdown.PreStop != nil {
		command := c.Shutdown.PreStop.Exec.Command
		if len(command) == 0 {
			problems = append(problems, "shutdown.pre_stop.exec.command must contain at least one argument")
		} else if len(command) > 64 {
			problems = append(problems, "shutdown.pre_stop.exec.command must contain at most 64 arguments")
		}
		for index, argument := range command {
			if strings.ContainsRune(argument, '\x00') || len(argument) > 4096 {
				problems = append(problems, fmt.Sprintf("shutdown.pre_stop.exec.command[%d] must be at most 4096 bytes and contain no NUL byte", index))
			}
		}
		if len(command) > 0 && strings.TrimSpace(command[0]) == "" {
			problems = append(problems, "shutdown.pre_stop.exec.command[0] must name an executable")
		}
	}
	if c.Traffic.PostSignal.Delay.Value() >= c.Shutdown.Deadline.Value() {
		problems = append(problems, "traffic.post_signal.delay must be less than shutdown.deadline")
	}
	positiveDuration(&problems, "assertions.readiness_withdrawn_within", c.Assertions.ReadinessWithdrawnWithin)
	if c.Assertions.MaxFailedRequests < 0 {
		problems = append(problems, "assertions.max_failed_requests must not be negative")
	}
	if c.Assertions.ReadinessWithdrawnWithin.Value() > c.Shutdown.Deadline.Value() {
		problems = append(problems, "assertions.readiness_withdrawn_within must not exceed shutdown.deadline")
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func positiveDuration(problems *[]string, name string, value Duration) {
	if value.IsZero() || value.Value() <= 0 {
		*problems = append(*problems, name+" must be greater than zero")
	}
}

func nonNegativeDuration(problems *[]string, name string, value Duration) {
	if value.IsZero() || value.Value() < 0 {
		*problems = append(*problems, name+" must not be negative")
	}
}

func optionalBudgetDuration(problems *[]string, name string, value Duration) {
	if value.IsZero() {
		return
	}
	if value.Value() < time.Millisecond {
		*problems = append(*problems, name+" must be at least 1ms")
	}
}

func optionalPort(problems *[]string, name string, value *int) {
	if value != nil && (*value < 1 || *value > 65535) {
		*problems = append(*problems, name+" must be between 1 and 65535 when set")
	}
}

func (c Config) ReadinessPort() int {
	return effectivePort(c.Readiness.ContainerPort, c.Target.ContainerPort)
}
func (c Config) TrafficPort() int {
	return effectivePort(c.Traffic.ContainerPort, c.Target.ContainerPort)
}
func (c Config) SSEPort() int {
	return effectivePort(c.Streaming.SSE.ContainerPort, c.Target.ContainerPort)
}
func (c Config) WebSocketPort() int {
	return effectivePort(c.Streaming.WebSocket.ContainerPort, c.Target.ContainerPort)
}
func (c Config) GRPCStreamPort() int {
	return effectivePort(c.Streaming.GRPC.ContainerPort, c.Target.ContainerPort)
}

// RequiredContainerPorts returns each enabled probe port once in deterministic order.
func (c Config) RequiredContainerPorts() []int {
	ports := map[int]struct{}{
		c.TrafficPort(): {},
	}
	if c.Readiness.Driver != ReadinessDriverExec {
		ports[c.ReadinessPort()] = struct{}{}
	}
	if c.Streaming.SSE.Enabled {
		ports[c.SSEPort()] = struct{}{}
	}
	if c.Streaming.WebSocket.Enabled {
		ports[c.WebSocketPort()] = struct{}{}
	}
	if c.Streaming.GRPC.Enabled {
		ports[c.GRPCStreamPort()] = struct{}{}
	}
	result := make([]int, 0, len(ports))
	for port := range ports {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}

func effectivePort(configured *int, fallback int) int {
	if configured != nil {
		return *configured
	}
	return fallback
}

func validWebSocketCloseCode(code int) bool {
	if code >= 3000 && code <= 4999 {
		return true
	}
	if code < 1000 || code > 1014 {
		return false
	}
	return code != 1004 && code != 1005 && code != 1006
}

func validWebSocketSubprotocol(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validateGRPCCall(problems *[]string, name, method, request, requestFile string, requestResolved bool, metadata map[string]string, descriptorSet string, descriptorReady bool) {
	if !validGRPCMethod(method) {
		*problems = append(*problems, name+".method must use package.Service/Method form")
	}
	if request != "" && strings.TrimSpace(requestFile) != "" {
		*problems = append(*problems, name+".request and "+name+".request_file are mutually exclusive")
	}
	if requestFile != "" && strings.TrimSpace(requestFile) == "" {
		*problems = append(*problems, name+".request_file must not be blank")
	}
	if strings.TrimSpace(requestFile) != "" && !requestResolved {
		*problems = append(*problems, name+".request_file was not resolved; load file-backed configurations with config.LoadFile")
	}
	if len(request) > maxRequestBodyBytes {
		*problems = append(*problems, fmt.Sprintf("%s.request must not exceed %d bytes", name, maxRequestBodyBytes))
	}
	if requestFile == "" && request != "" && !json.Valid([]byte(request)) {
		*problems = append(*problems, name+".request must be valid JSON")
	}
	if strings.TrimSpace(descriptorSet) != "" && !descriptorReady {
		*problems = append(*problems, name+".descriptor_set was not resolved; load file-backed configurations with config.LoadFile")
	}
	if len(metadata) > 64 {
		*problems = append(*problems, name+".metadata must contain at most 64 values")
	}
	for key, value := range metadata {
		if !validGRPCMetadataKey(key) {
			*problems = append(*problems, fmt.Sprintf("%s.metadata contains invalid key %q", name, key))
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			*problems = append(*problems, fmt.Sprintf("%s.metadata contains invalid value for %q", name, key))
		}
	}
}

func validateGRPCCodes(problems *[]string, name string, values []string) {
	if len(values) == 0 {
		*problems = append(*problems, name+" must contain at least one gRPC status code")
		return
	}
	if len(values) > 17 {
		*problems = append(*problems, name+" must contain at most 17 gRPC status codes")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validGRPCCode(value) {
			*problems = append(*problems, fmt.Sprintf("%s contains unknown gRPC status code %q", name, value))
			continue
		}
		if _, found := seen[value]; found {
			*problems = append(*problems, fmt.Sprintf("%s contains duplicate gRPC status code %q", name, value))
		}
		seen[value] = struct{}{}
	}
}

func validGRPCMethod(value string) bool {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	service, method, found := strings.Cut(value, "/")
	return found && service != "" && method != "" && !strings.Contains(method, "/") && validProtoName(service) && validProtoIdentifier(method)
}

func validProtoName(value string) bool {
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if !validProtoIdentifier(part) {
			return false
		}
	}
	return len(parts) > 0
}

func validProtoIdentifier(value string) bool {
	if value == "" || value[0] >= '0' && value[0] <= '9' {
		return false
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validGRPCMetadataKey(value string) bool {
	if value == "" || strings.HasPrefix(value, ":") || strings.HasPrefix(strings.ToLower(value), "grpc-") {
		return false
	}
	for _, character := range []byte(value) {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func validGRPCCode(value string) bool {
	switch value {
	case "OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED":
		return true
	default:
		return false
	}
}
