package report

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

type Report struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	Image         string           `json:"image"`
	Runtime       string           `json:"runtime"`
	Profile       string           `json:"profile"`
	StartedAt     time.Time        `json:"started_at"`
	DurationMS    int64            `json:"duration_ms"`
	Passed        bool             `json:"passed"`
	Events        []Event          `json:"events"`
	Assertions    []Assertion      `json:"assertions"`
	Traffic       TrafficSummary   `json:"traffic"`
	Streaming     StreamingSummary `json:"streaming"`
	Telemetry     TelemetrySummary `json:"telemetry"`
	Shutdown      ShutdownSummary  `json:"shutdown"`
	Timings       TimingSummary    `json:"timings"`
	Container     ContainerSummary `json:"container"`
	Logs          string           `json:"logs,omitempty"`
	ForcedCleanup bool             `json:"forced_cleanup"`
	Retained      string           `json:"retained_container,omitempty"`
	startedMono   time.Time
}

type Event struct {
	ElapsedMS int64  `json:"elapsed_ms"`
	Phase     string `json:"phase"`
	Message   string `json:"message"`
}

type Assertion struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type TrafficSummary struct {
	Driver      string                   `json:"driver"`
	Configured  int                      `json:"configured"`
	Started     int                      `json:"started"`
	Inflight    int                      `json:"inflight_at_signal"`
	Completed   int                      `json:"completed"`
	Successful  int                      `json:"successful"`
	Failed      int                      `json:"failed"`
	InflightOK  int                      `json:"successful_inflight"`
	InflightBad int                      `json:"failed_inflight"`
	PostSignal  PostSignalTrafficSummary `json:"post_signal"`
}

type PostSignalTrafficSummary struct {
	Policy     string `json:"policy"`
	Configured int    `json:"configured"`
	Started    int    `json:"started"`
	Completed  int    `json:"completed"`
	Accepted   int    `json:"accepted"`
	Rejected   int    `json:"rejected"`
}

type StreamingSummary struct {
	SSE       SSESummary        `json:"sse"`
	WebSocket WebSocketSummary  `json:"websocket"`
	GRPC      GRPCStreamSummary `json:"grpc"`
}

type SSESummary struct {
	Enabled               bool   `json:"enabled"`
	Established           bool   `json:"established"`
	Status                int    `json:"status"`
	ContentType           string `json:"content_type"`
	InitialEventReceived  bool   `json:"initial_event_received"`
	ActiveAtSignal        bool   `json:"active_at_signal"`
	TerminalEventReceived bool   `json:"terminal_event_received"`
	Events                int    `json:"events"`
	CleanEOF              bool   `json:"clean_eof"`
	ClosedAfterSignal     bool   `json:"closed_after_signal"`
	ClosedWithinTimeout   bool   `json:"closed_within_timeout"`
	ClosedGracefully      bool   `json:"closed_gracefully"`
	ErrorKind             string `json:"error_kind"`
	Error                 string `json:"error"`
}

type WebSocketSummary struct {
	Enabled                    bool   `json:"enabled"`
	Established                bool   `json:"established"`
	Status                     int    `json:"status"`
	NegotiatedSubprotocol      string `json:"negotiated_subprotocol"`
	ActiveAtSignal             bool   `json:"active_at_signal"`
	Messages                   int    `json:"messages"`
	TerminalMessageReceived    bool   `json:"terminal_message_received"`
	TerminalMessageAfterSignal bool   `json:"terminal_message_after_signal"`
	CloseFrameReceived         bool   `json:"close_frame_received"`
	CloseCode                  int    `json:"close_code"`
	CloseReason                string `json:"close_reason"`
	ClosedAfterSignal          bool   `json:"closed_after_signal"`
	ClosedWithinTimeout        bool   `json:"closed_within_timeout"`
	ClosedGracefully           bool   `json:"closed_gracefully"`
	ErrorKind                  string `json:"error_kind"`
	Error                      string `json:"error"`
}

type GRPCStreamSummary struct {
	Enabled             bool   `json:"enabled"`
	Established         bool   `json:"established"`
	ActiveAtSignal      bool   `json:"active_at_signal"`
	Messages            int    `json:"messages"`
	FinalCode           string `json:"final_code"`
	ClosedAfterSignal   bool   `json:"closed_after_signal"`
	ClosedWithinTimeout bool   `json:"closed_within_timeout"`
	ClosedGracefully    bool   `json:"closed_gracefully"`
	ErrorKind           string `json:"error_kind"`
	Error               string `json:"error"`
}

type TelemetrySummary struct {
	Enabled                bool                   `json:"enabled"`
	Protocol               string                 `json:"protocol"`
	EligibleInflight       int                    `json:"eligible_inflight_requests"`
	MinimumCorrelatedSpans int                    `json:"minimum_correlated_spans"`
	CorrelatedSpans        int                    `json:"correlated_spans"`
	MatchedRequests        int                    `json:"matched_requests"`
	ExportRequests         int                    `json:"export_requests"`
	RejectedExportRequests int                    `json:"rejected_export_requests"`
	Metrics                MetricTelemetrySummary `json:"metrics"`
}

type MetricTelemetrySummary struct {
	Enabled           bool `json:"enabled"`
	MinimumDataPoints int  `json:"minimum_data_points"`
	DataPoints        int  `json:"data_points"`
	ExportRequests    int  `json:"export_requests"`
}

type ShutdownSummary struct {
	DeadlineMS int64          `json:"deadline_ms"`
	PreStop    PreStopSummary `json:"pre_stop"`
}

type PreStopSummary struct {
	Configured bool  `json:"configured"`
	ExitCode   int   `json:"exit_code"`
	DurationMS int64 `json:"duration_ms"`
	TimedOut   bool  `json:"timed_out"`
}

// TimingSummary records end-to-end lifecycle milestones observed by Draincheck. Startup is
// measured from verification start; pre-stop is its command duration; signal, readiness, and
// container exit are measured from the signal request; shutdown total starts before pre-stop.
type TimingSummary struct {
	StartupReadyMS        int64 `json:"startup_ready_ms"`
	PreStopMS             int64 `json:"pre_stop_ms"`
	SignalDeliveryMS      int64 `json:"signal_delivery_ms"`
	ReadinessWithdrawalMS int64 `json:"readiness_withdrawal_ms"`
	ContainerExitMS       int64 `json:"container_exit_ms"`
	ShutdownTotalMS       int64 `json:"shutdown_total_ms"`
}

type ContainerSummary struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Status    string `json:"status,omitempty"`
	ExitCode  int    `json:"exit_code"`
	OOMKilled bool   `json:"oom_killed"`
	Error     string `json:"error,omitempty"`
}

func New(runID, image, runtimeName string, now time.Time) *Report {
	return &Report{
		SchemaVersion: SchemaVersion,
		RunID:         runID,
		Image:         image,
		Runtime:       runtimeName,
		Profile:       "generic",
		StartedAt:     now.UTC(),
		Events:        make([]Event, 0, 16),
		Assertions:    make([]Assertion, 0, 12),
		startedMono:   now,
	}
}

func (r *Report) AddEvent(now time.Time, phase, message string) {
	r.Events = append(r.Events, Event{
		ElapsedMS: now.Sub(r.startedMono).Milliseconds(),
		Phase:     phase,
		Message:   message,
	})
}

func (r *Report) AddAssertion(name string, passed bool, message string) {
	r.Assertions = append(r.Assertions, Assertion{Name: name, Passed: passed, Message: message})
}

func (r *Report) Finish(now time.Time) {
	r.DurationMS = now.Sub(r.startedMono).Milliseconds()
	sort.SliceStable(r.Events, func(i, j int) bool {
		return r.Events[i].ElapsedMS < r.Events[j].ElapsedMS
	})
	r.Passed = len(r.Assertions) > 0
	for _, assertion := range r.Assertions {
		if !assertion.Passed {
			r.Passed = false
			return
		}
	}
}

func (r *Report) WriteHuman(writer io.Writer) {
	verdict := "PASS"
	if !r.Passed {
		verdict = "FAIL"
	}
	_, _ = fmt.Fprintf(writer, "%s %s (%s)\n\n", verdict, r.Image, time.Duration(r.DurationMS)*time.Millisecond)
	for _, event := range r.Events {
		_, _ = fmt.Fprintf(writer, "%7.3fs  %-12s %s\n", float64(event.ElapsedMS)/1000, event.Phase, event.Message)
	}

	failed := r.FailedAssertions()
	if len(failed) > 0 {
		_, _ = fmt.Fprintln(writer, "\nFailed assertions:")
		for _, assertion := range failed {
			_, _ = fmt.Fprintf(writer, "  - %s: %s\n", assertion.Name, assertion.Message)
		}
		if hint := diagnosticHint(failed); hint != "" {
			_, _ = fmt.Fprintf(writer, "\nHint: %s\n", hint)
		}
	}
	if r.Retained != "" {
		_, _ = fmt.Fprintf(writer, "\nRetained container: %s\n", r.Retained)
	}
	if !r.Passed && strings.TrimSpace(r.Logs) != "" {
		_, _ = fmt.Fprintln(writer, "\nContainer logs:")
		_, _ = fmt.Fprintln(writer, strings.TrimSpace(r.Logs))
	}
}

func (r *Report) FailedAssertions() []Assertion {
	failed := make([]Assertion, 0)
	for _, assertion := range r.Assertions {
		if !assertion.Passed {
			failed = append(failed, assertion)
		}
	}
	return failed
}

func diagnosticHint(failed []Assertion) string {
	priorities := []string{
		"shutdown.pre_stop",
		"shutdown.deadline",
		"shutdown.force_kill",
		"grpc_stream.closed_gracefully",
		"grpc_stream.active_at_signal",
		"grpc_stream.established",
		"websocket.closed_gracefully",
		"websocket.active_at_signal",
		"websocket.established",
		"stream.closed_gracefully",
		"stream.active_at_signal",
		"stream.established",
		"traffic.inflight_complete",
		"traffic.failed_requests",
		"traffic.post_signal_policy",
		"telemetry.spans_exported",
		"telemetry.metrics_exported",
		"traffic.inflight_exercised",
		"readiness.withdrawn",
		"startup.ready",
	}
	for _, name := range priorities {
		for _, assertion := range failed {
			if assertion.Name != name {
				continue
			}
			switch name {
			case "shutdown.deadline", "shutdown.force_kill":
				return "verify that PID 1 receives SIGTERM and the server has a bounded graceful-shutdown path."
			case "shutdown.pre_stop":
				return "make the pre-stop command fast, idempotent, and successful within the shared shutdown deadline."
			case "grpc_stream.closed_gracefully":
				return "finish the server-streaming RPC with the configured gRPC status before streaming.grpc.close_timeout."
			case "grpc_stream.active_at_signal":
				return "keep the server-streaming RPC open until the termination signal begins the shutdown transition."
			case "grpc_stream.established":
				return "verify the gRPC method, request JSON, metadata, descriptor source, and initial response behavior."
			case "websocket.closed_gracefully":
				return "send the configured terminal WebSocket message and close frame before streaming.websocket.close_timeout."
			case "websocket.active_at_signal":
				return "keep the WebSocket connection open until the termination signal begins the shutdown transition."
			case "websocket.established":
				return "verify the WebSocket path, opening handshake, headers, and configured subprotocols."
			case "stream.closed_gracefully":
				return "emit the configured terminal SSE event and close the stream cleanly before streaming.sse.close_timeout."
			case "stream.active_at_signal":
				return "keep the SSE connection open until the termination signal begins the shutdown transition."
			case "stream.established":
				return "verify the SSE path, response status, text/event-stream content type, and configured initial event."
			case "traffic.inflight_complete":
				return "keep listeners and dependencies available until active handlers have completed."
			case "traffic.failed_requests":
				if strings.Contains(assertion.Message, "protocol_") || strings.Contains(assertion.Message, "command_") || strings.Contains(assertion.Message, "probe_result") || strings.Contains(assertion.Message, "exit_code") {
					return "inspect the command probe protocol: emit one active event, then one result event, and exit zero within traffic.request_timeout."
				}
				if strings.Contains(assertion.Message, "gRPC ") {
					return "inspect the gRPC status, method contract, and whether the server keeps active RPCs alive during shutdown."
				}
				return "inspect application shutdown ordering; requests failed while the lifecycle transition was in progress."
			case "traffic.post_signal_policy":
				return "align traffic.post_signal.policy and delay with when the application stops accepting new work."
			case "telemetry.spans_exported":
				return "flush the OpenTelemetry tracer provider during shutdown after in-flight handlers complete."
			case "telemetry.metrics_exported":
				return "flush the OpenTelemetry meter provider during shutdown after in-flight handlers complete."
			case "traffic.inflight_exercised":
				if strings.Contains(assertion.Message, "command probes") {
					return "make the command probe emit its active event only after the external operation is confirmed in flight."
				}
				if strings.Contains(assertion.Message, "prepare gRPC") {
					return "verify the gRPC method, protobuf JSON request, and reflection or descriptor-set source."
				}
				return "use a request that remains active long enough for the runtime to confirm signal delivery."
			case "readiness.withdrawn":
				return "mark the service unready as soon as shutdown begins so new traffic stops arriving."
			case "startup.ready":
				return "inspect the image entrypoint, container port, readiness path, and startup logs."
			}
		}
	}
	for _, assertion := range failed {
		if assertion.Name == "execution.completed" || assertion.Name == "cleanup.completed" {
			return "check runtime availability, permissions, and the reported command error."
		}
	}
	return "review the event timeline and bounded container logs above."
}
