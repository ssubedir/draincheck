package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type conformanceReport struct {
	SchemaVersion int                    `json:"schema_version"`
	RunID         string                 `json:"run_id"`
	Runtime       string                 `json:"runtime"`
	Profile       string                 `json:"profile"`
	Passed        bool                   `json:"passed"`
	Events        []conformanceEvent     `json:"events"`
	Assertions    []conformanceAssertion `json:"assertions"`
	Traffic       conformanceTraffic     `json:"traffic"`
	Streaming     conformanceStreaming   `json:"streaming"`
	Telemetry     conformanceTelemetry   `json:"telemetry"`
	Shutdown      conformanceShutdown    `json:"shutdown"`
	Timings       conformanceTimings     `json:"timings"`
}

type conformanceShutdown struct {
	DeadlineMS int64              `json:"deadline_ms"`
	PreStop    conformancePreStop `json:"pre_stop"`
}

type conformancePreStop struct {
	Configured bool  `json:"configured"`
	ExitCode   int   `json:"exit_code"`
	DurationMS int64 `json:"duration_ms"`
	TimedOut   bool  `json:"timed_out"`
}

type conformanceEvent struct {
	Phase   string `json:"phase"`
	Message string `json:"message"`
}

type conformanceAssertion struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message"`
}

type conformanceTraffic struct {
	Driver     string                       `json:"driver"`
	Inflight   int                          `json:"inflight_at_signal"`
	Successful int                          `json:"successful"`
	Failed     int                          `json:"failed"`
	PostSignal conformancePostSignalTraffic `json:"post_signal"`
}

type conformancePostSignalTraffic struct {
	Policy     string `json:"policy"`
	Configured int    `json:"configured"`
	Completed  int    `json:"completed"`
	Accepted   int    `json:"accepted"`
	Rejected   int    `json:"rejected"`
}

type conformanceStreaming struct {
	SSE       conformanceSSE        `json:"sse"`
	WebSocket conformanceWebSocket  `json:"websocket"`
	GRPC      conformanceGRPCStream `json:"grpc"`
}

type conformanceSSE struct {
	Enabled               bool `json:"enabled"`
	Established           bool `json:"established"`
	InitialEventReceived  bool `json:"initial_event_received"`
	ActiveAtSignal        bool `json:"active_at_signal"`
	TerminalEventReceived bool `json:"terminal_event_received"`
	Events                int  `json:"events"`
	CleanEOF              bool `json:"clean_eof"`
	ClosedAfterSignal     bool `json:"closed_after_signal"`
	ClosedWithinTimeout   bool `json:"closed_within_timeout"`
	ClosedGracefully      bool `json:"closed_gracefully"`
}

type conformanceWebSocket struct {
	Enabled                    bool `json:"enabled"`
	Established                bool `json:"established"`
	ActiveAtSignal             bool `json:"active_at_signal"`
	Messages                   int  `json:"messages"`
	TerminalMessageReceived    bool `json:"terminal_message_received"`
	TerminalMessageAfterSignal bool `json:"terminal_message_after_signal"`
	CloseFrameReceived         bool `json:"close_frame_received"`
	CloseCode                  int  `json:"close_code"`
	ClosedAfterSignal          bool `json:"closed_after_signal"`
	ClosedWithinTimeout        bool `json:"closed_within_timeout"`
	ClosedGracefully           bool `json:"closed_gracefully"`
}

type conformanceGRPCStream struct {
	Enabled             bool   `json:"enabled"`
	Established         bool   `json:"established"`
	ActiveAtSignal      bool   `json:"active_at_signal"`
	Messages            int    `json:"messages"`
	FinalCode           string `json:"final_code"`
	ClosedAfterSignal   bool   `json:"closed_after_signal"`
	ClosedWithinTimeout bool   `json:"closed_within_timeout"`
	ClosedGracefully    bool   `json:"closed_gracefully"`
}

type conformanceTelemetry struct {
	Enabled                bool                       `json:"enabled"`
	Protocol               string                     `json:"protocol"`
	EligibleInflight       int                        `json:"eligible_inflight_requests"`
	MinimumCorrelatedSpans int                        `json:"minimum_correlated_spans"`
	CorrelatedSpans        int                        `json:"correlated_spans"`
	MatchedRequests        int                        `json:"matched_requests"`
	ExportRequests         int                        `json:"export_requests"`
	RejectedExportRequests int                        `json:"rejected_export_requests"`
	Metrics                conformanceMetricTelemetry `json:"metrics"`
}

type conformanceMetricTelemetry struct {
	Enabled           bool `json:"enabled"`
	MinimumDataPoints int  `json:"minimum_data_points"`
	DataPoints        int  `json:"data_points"`
	ExportRequests    int  `json:"export_requests"`
}

type conformanceTimings struct {
	StartupReadyMS        int64 `json:"startup_ready_ms"`
	SignalDeliveryMS      int64 `json:"signal_delivery_ms"`
	ReadinessWithdrawalMS int64 `json:"readiness_withdrawal_ms"`
	ContainerExitMS       int64 `json:"container_exit_ms"`
}

type repeatConformanceSummary struct {
	SchemaVersion  int  `json:"schema_version"`
	RunsRequested  int  `json:"runs_requested"`
	RunsCompleted  int  `json:"runs_completed"`
	RunsPassed     int  `json:"runs_passed"`
	RunsFailed     int  `json:"runs_failed"`
	BudgetFailures int  `json:"budget_failures"`
	Passed         bool `json:"passed"`
	Runs           []struct {
		RunID             string `json:"run_id"`
		ArtifactDirectory string `json:"artifact_directory"`
	} `json:"runs"`
	Timings struct {
		Verification struct {
			Samples int `json:"samples"`
		} `json:"verification"`
	} `json:"timings"`
	BudgetAssertions []struct {
		Name          string `json:"name"`
		Evaluated     bool   `json:"evaluated"`
		Passed        bool   `json:"passed"`
		Samples       int    `json:"samples"`
		ObservedP95MS int64  `json:"observed_p95_ms"`
		LimitMS       int64  `json:"limit_ms"`
	} `json:"budget_assertions"`
}

type suiteConformanceSummary struct {
	SchemaVersion      int  `json:"schema_version"`
	ScenariosRequested int  `json:"scenarios_requested"`
	ScenariosCompleted int  `json:"scenarios_completed"`
	ScenariosPassed    int  `json:"scenarios_passed"`
	ScenariosFailed    int  `json:"scenarios_failed"`
	ExecutionErrors    int  `json:"execution_errors"`
	Passed             bool `json:"passed"`
	Scenarios          []struct {
		Name              string   `json:"name"`
		RunID             string   `json:"run_id"`
		Passed            bool     `json:"passed"`
		FailedAssertions  []string `json:"failed_assertions"`
		ArtifactDirectory string   `json:"artifact_directory"`
	} `json:"scenarios"`
}

type conformanceJUnit struct {
	XMLName  xml.Name `xml:"testsuite"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Errors   int      `xml:"errors,attr"`
	Skipped  int      `xml:"skipped,attr"`
}

func TestLifecycleConformance(t *testing.T) {
	runtimeName := environment("DRAINCHECK_E2E_RUNTIME", "docker")
	if runtimeName != "docker" && runtimeName != "podman" {
		t.Fatalf("DRAINCHECK_E2E_RUNTIME must be docker or podman, got %q", runtimeName)
	}
	runtimeBinary, err := exec.LookPath(runtimeName)
	if err != nil {
		t.Fatalf("find %s: %v", runtimeName, err)
	}
	runCommand(t, "check container runtime", "", runtimeBinary, "info")

	root := repositoryRoot(t)
	temporary := t.TempDir()
	reportDirectory := os.Getenv("DRAINCHECK_E2E_REPORT_DIR")
	if reportDirectory == "" {
		reportDirectory = filepath.Join(temporary, "reports")
	} else if !filepath.IsAbs(reportDirectory) {
		reportDirectory = filepath.Join(root, reportDirectory)
	}
	if err := os.MkdirAll(reportDirectory, 0o755); err != nil {
		t.Fatalf("create report directory: %v", err)
	}

	binaryName := "draincheck"
	if goruntime.GOOS == "windows" {
		binaryName += ".exe"
	}
	draincheckBinary := filepath.Join(temporary, binaryName)
	runCommand(t, "build Draincheck", root, "go", "build", "-o", draincheckBinary, "./cmd/draincheck")
	probeBinaryName := "draincheck-probe"
	if goruntime.GOOS == "windows" {
		probeBinaryName += ".exe"
	}
	probeBinary := filepath.Join(temporary, probeBinaryName)
	runCommand(t, "build command probe fixture", root, "go", "build", "-o", probeBinary, "./testdata/probes/fixture")

	imageSuffix := time.Now().UnixNano()
	image := fmt.Sprintf("draincheck-conformance:%d", imageSuffix)
	runCommand(t, "build fixture image", root, runtimeBinary,
		"build",
		"-f", filepath.Join(root, "testdata", "services", "good-http", "Dockerfile"),
		"-t", image,
		filepath.Join(root, "testdata", "services"),
	)
	nonforwardingImage := fmt.Sprintf("draincheck-nonforwarding-pid1:%d", imageSuffix)
	runCommand(t, "build non-forwarding PID 1 fixture image", root, runtimeBinary,
		"build",
		"-f", filepath.Join(root, "testdata", "services", "nonforwarding-pid1", "Dockerfile"),
		"-t", nonforwardingImage,
		filepath.Join(root, "testdata", "services"),
	)
	t.Cleanup(func() {
		for _, fixtureImage := range []string{image, nonforwardingImage} {
			command := exec.Command(runtimeBinary, "image", "rm", "--force", fixtureImage)
			if output, err := command.CombinedOutput(); err != nil && !strings.Contains(strings.ToLower(string(output)), "no such image") {
				t.Logf("remove fixture image %s: %v\n%s", fixtureImage, err, output)
			}
		}
	})

	cases := []struct {
		name             string
		artifact         string
		image            string
		mode             string
		exitCode         int
		passed           bool
		signalExpected   bool
		confirmedTraffic bool
		missingSignalLog bool
		postSignalPolicy string
		postAccepted     int
		postRejected     int
		telemetryEnabled bool
		telemetryPassed  bool
		metricsEnabled   bool
		metricsPassed    bool
		streamingEnabled bool
		streamingPassed  bool
		webSocketEnabled bool
		webSocketPassed  bool
		grpcTraffic      bool
		grpcStreaming    bool
		grpcStreamPassed bool
		richTraffic      bool
		commandTraffic   bool
		profile          string
		preStopExpected  bool
		preStopExitCode  int
		failedAssertions []string
	}{
		{name: "graceful", artifact: "graceful", image: image, mode: "graceful", exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "ignored signal", artifact: "ignore", image: image, mode: "ignore", exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"shutdown.deadline", "shutdown.force_kill"}},
		{name: "PID 1 wrapper does not forward signal", artifact: "nonforwarding-pid1", image: nonforwardingImage, mode: "graceful", exitCode: 1, signalExpected: true, confirmedTraffic: true, missingSignalLog: true, failedAssertions: []string{"readiness.withdrawn", "shutdown.deadline", "shutdown.force_kill"}},
		{name: "never ready", artifact: "never-ready", image: image, mode: "never-ready", exitCode: 1, failedAssertions: []string{"startup.ready"}},
		{name: "exit before ready", artifact: "exit-before-ready", image: image, mode: "exit-before-ready", exitCode: 1, failedAssertions: []string{"startup.ready"}},
		{name: "dropped in-flight requests", artifact: "drop-inflight", image: image, mode: "drop-inflight", exitCode: 1, signalExpected: true, failedAssertions: []string{"traffic.failed_requests"}},
		{name: "stale readiness", artifact: "stale-readiness", image: image, mode: "stale-readiness", exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"readiness.withdrawn"}},
		{name: "non-zero exit", artifact: "nonzero-exit", image: image, mode: "nonzero-exit", exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"shutdown.exit_code"}},
		{name: "accept post-signal traffic", artifact: "post-signal-accept", image: image, mode: "post-signal-accept", postSignalPolicy: "accept", postAccepted: 2, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "reject post-signal traffic", artifact: "post-signal-reject", image: image, mode: "post-signal-reject", postSignalPolicy: "reject", postRejected: 2, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "post-signal policy mismatch", artifact: "post-signal-mismatch", image: image, mode: "post-signal-accept", postSignalPolicy: "reject", postAccepted: 2, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"traffic.post_signal_policy"}},
		{name: "OpenTelemetry shutdown flush", artifact: "telemetry-flush", image: image, mode: "telemetry-flush", telemetryEnabled: true, telemetryPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "missing OpenTelemetry shutdown flush", artifact: "telemetry-drop", image: image, mode: "telemetry-drop", telemetryEnabled: true, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"telemetry.spans_exported"}},
		{name: "OpenTelemetry metrics shutdown flush", artifact: "metrics-flush", image: image, mode: "metrics-flush", metricsEnabled: true, metricsPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "missing OpenTelemetry metrics shutdown flush", artifact: "metrics-drop", image: image, mode: "metrics-drop", metricsEnabled: true, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"telemetry.metrics_exported"}},
		{name: "graceful SSE shutdown", artifact: "sse-graceful", image: image, mode: "sse-graceful", streamingEnabled: true, streamingPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "SSE closes without terminal event", artifact: "sse-drop", image: image, mode: "sse-drop", streamingEnabled: true, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"stream.closed_gracefully"}},
		{name: "graceful WebSocket shutdown", artifact: "websocket-graceful", image: image, mode: "websocket-graceful", webSocketEnabled: true, webSocketPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "WebSocket closes without terminal message", artifact: "websocket-drop", image: image, mode: "websocket-drop", webSocketEnabled: true, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"websocket.closed_gracefully"}},
		{name: "gRPC unary and stream drain gracefully", artifact: "grpc-graceful", image: image, mode: "grpc-graceful", grpcTraffic: true, grpcStreaming: true, grpcStreamPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "gRPC health drives readiness", artifact: "grpc-readiness", image: image, mode: "grpc-readiness", grpcTraffic: true, grpcStreaming: true, grpcStreamPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "container exec drives readiness", artifact: "exec-readiness", image: image, mode: "exec-readiness", exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "Kubernetes pre-stop runs before SIGTERM", artifact: "kubernetes-prestop", image: image, mode: "kubernetes-prestop", profile: "kubernetes", preStopExpected: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "failed Kubernetes pre-stop is reported", artifact: "kubernetes-prestop-fail", image: image, mode: "kubernetes-prestop-fail", profile: "kubernetes", preStopExpected: true, preStopExitCode: 7, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"shutdown.pre_stop"}},
		{name: "readiness and gRPC use separate ports", artifact: "grpc-multiport", image: image, mode: "grpc-multiport", grpcTraffic: true, grpcStreaming: true, grpcStreamPassed: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "gRPC stream ends with unexpected status", artifact: "grpc-stream-drop", image: image, mode: "grpc-stream-drop", grpcTraffic: true, grpcStreaming: true, exitCode: 1, signalExpected: true, confirmedTraffic: true, failedAssertions: []string{"grpc_stream.closed_gracefully"}},
		{name: "POST body and custom success status", artifact: "rich-http", image: image, mode: "rich-http", postSignalPolicy: "accept", postAccepted: 2, richTraffic: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
		{name: "user-provided command probe", artifact: "command-probe", image: image, mode: "post-signal-accept", postSignalPolicy: "accept", postAccepted: 2, commandTraffic: true, exitCode: 0, passed: true, signalExpected: true, confirmedTraffic: true},
	}
	if err := os.WriteFile(filepath.Join(temporary, "rich-request.json"), []byte(`{"task":"drain"}`), 0o600); err != nil {
		t.Fatalf("write rich request body: %v", err)
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(temporary, test.artifact+".yaml")
			if err := os.WriteFile(configPath, []byte(conformanceConfigForCase(test.mode, test.postSignalPolicy, test.telemetryEnabled, test.metricsEnabled, test.streamingEnabled, test.webSocketEnabled, test.richTraffic, test.commandTraffic, test.grpcTraffic, test.grpcStreaming, probeBinaryName)), 0o600); err != nil {
				t.Fatalf("write configuration: %v", err)
			}
			jsonPath := filepath.Join(reportDirectory, test.artifact+".json")
			junitPath := filepath.Join(reportDirectory, test.artifact+".xml")
			debugPath := filepath.Join(reportDirectory, test.artifact+"-debug.zip")

			arguments := []string{
				"verify", test.image,
				"--runtime", runtimeName,
				"--config", configPath,
				"--report-json", jsonPath,
				"--report-junit", junitPath,
				"--debug-bundle", debugPath,
				"--no-color",
			}
			if test.profile != "" {
				arguments = append(arguments, "--profile", test.profile)
			}
			command := exec.Command(
				draincheckBinary,
				arguments...,
			)
			command.Dir = root
			output, runErr := command.CombinedOutput()
			if exitCode(runErr) != test.exitCode {
				t.Errorf("exit code = %d, want %d\n%s", exitCode(runErr), test.exitCode, output)
			}

			report := readReport(t, jsonPath)
			expectedDriver := "http"
			if test.commandTraffic {
				expectedDriver = "command"
			} else if test.grpcTraffic {
				expectedDriver = "grpc"
			}
			if report.Traffic.Driver != expectedDriver {
				t.Errorf("traffic driver = %q, want %q\n%s", report.Traffic.Driver, expectedDriver, output)
			}
			if report.SchemaVersion != 1 {
				t.Errorf("report schema version = %d, want 1", report.SchemaVersion)
			}
			if report.Runtime != runtimeName {
				t.Errorf("report runtime = %q, want %q", report.Runtime, runtimeName)
			}
			expectedProfile := "generic"
			if test.profile != "" {
				expectedProfile = test.profile
			}
			if report.Profile != expectedProfile {
				t.Errorf("report profile = %q, want %q", report.Profile, expectedProfile)
			}
			if report.Passed != test.passed {
				t.Errorf("report passed = %t, want %t\n%s", report.Passed, test.passed, output)
			}
			assertFailedAssertions(t, report, test.failedAssertions, output)
			if test.signalExpected {
				assertSignalEvidence(t, report, test.confirmedTraffic, output)
			}
			if test.mode == "exec-readiness" {
				assertExecReadinessEvidence(t, report, output)
			}
			if test.preStopExpected {
				preStop := report.Shutdown.PreStop
				if report.Shutdown.DeadlineMS != 30000 || !preStop.Configured || preStop.ExitCode != test.preStopExitCode || preStop.TimedOut || preStop.DurationMS < 1 {
					t.Errorf("Kubernetes pre-stop evidence = %#v/%#v, want deadline=30000ms exit=%d\n%s", report.Shutdown, preStop, test.preStopExitCode, output)
				}
			}
			if test.postSignalPolicy != "" {
				postSignal := report.Traffic.PostSignal
				if postSignal.Policy != test.postSignalPolicy || postSignal.Configured != 2 || postSignal.Completed != 2 || postSignal.Accepted != test.postAccepted || postSignal.Rejected != test.postRejected {
					t.Errorf("post-signal traffic = %#v, want policy=%s configured/completed=2 accepted=%d rejected=%d\n%s", postSignal, test.postSignalPolicy, test.postAccepted, test.postRejected, output)
				}
			}
			if test.telemetryEnabled || test.metricsEnabled {
				telemetry := report.Telemetry
				if telemetry.Protocol != "http/protobuf" || telemetry.EligibleInflight < 1 || telemetry.RejectedExportRequests != 0 {
					t.Errorf("telemetry summary has invalid contract evidence: %#v\n%s", telemetry, output)
				}
			}
			if test.telemetryEnabled {
				telemetry := report.Telemetry
				if !telemetry.Enabled || telemetry.MinimumCorrelatedSpans != 1 {
					t.Errorf("minimum correlated spans = %d, want 1\n%s", telemetry.MinimumCorrelatedSpans, output)
				}
				if test.telemetryPassed && (telemetry.CorrelatedSpans < 1 || telemetry.MatchedRequests < 1 || telemetry.ExportRequests < 1) {
					t.Errorf("telemetry flush evidence = %#v, want correlated export\n%s", telemetry, output)
				}
				if !test.telemetryPassed && (telemetry.CorrelatedSpans != 0 || telemetry.MatchedRequests != 0) {
					t.Errorf("missing-flush telemetry evidence = %#v, want no correlated spans\n%s", telemetry, output)
				}
			}
			if test.metricsEnabled {
				metrics := report.Telemetry.Metrics
				if !metrics.Enabled || metrics.MinimumDataPoints != 1 {
					t.Errorf("metric telemetry summary has invalid contract evidence: %#v\n%s", metrics, output)
				}
				if test.metricsPassed && (metrics.DataPoints < 1 || metrics.ExportRequests < 1) {
					t.Errorf("metric flush evidence = %#v, want post-work data points\n%s", metrics, output)
				}
				if !test.metricsPassed && metrics.DataPoints != 0 {
					t.Errorf("missing metric flush evidence = %#v, want no post-work data points\n%s", metrics, output)
				}
			}
			if test.streamingEnabled {
				sse := report.Streaming.SSE
				if !sse.Enabled || !sse.Established || !sse.InitialEventReceived || !sse.ActiveAtSignal || sse.Events < 1 || !sse.ClosedAfterSignal || !sse.ClosedWithinTimeout || !sse.CleanEOF {
					t.Errorf("SSE lifecycle evidence is incomplete: %#v\n%s", sse, output)
				}
				if test.streamingPassed && (!sse.TerminalEventReceived || !sse.ClosedGracefully || sse.Events < 2) {
					t.Errorf("graceful SSE evidence = %#v, want terminal event and clean close\n%s", sse, output)
				}
				if !test.streamingPassed && (sse.TerminalEventReceived || sse.ClosedGracefully) {
					t.Errorf("broken SSE evidence = %#v, want missing terminal event\n%s", sse, output)
				}
			}
			if test.webSocketEnabled {
				webSocket := report.Streaming.WebSocket
				if !webSocket.Enabled || !webSocket.Established || !webSocket.ActiveAtSignal || !webSocket.CloseFrameReceived || webSocket.CloseCode != 1000 || !webSocket.ClosedAfterSignal || !webSocket.ClosedWithinTimeout {
					t.Errorf("WebSocket lifecycle evidence is incomplete: %#v\n%s", webSocket, output)
				}
				if test.webSocketPassed && (!webSocket.TerminalMessageReceived || !webSocket.TerminalMessageAfterSignal || webSocket.Messages < 1 || !webSocket.ClosedGracefully) {
					t.Errorf("graceful WebSocket evidence = %#v, want terminal message and clean close\n%s", webSocket, output)
				}
				if !test.webSocketPassed && (webSocket.TerminalMessageReceived || webSocket.TerminalMessageAfterSignal || webSocket.ClosedGracefully) {
					t.Errorf("broken WebSocket evidence = %#v, want missing terminal message\n%s", webSocket, output)
				}
			}
			if test.grpcStreaming {
				stream := report.Streaming.GRPC
				if !stream.Enabled || !stream.Established || !stream.ActiveAtSignal || stream.Messages < 1 || !stream.ClosedAfterSignal || !stream.ClosedWithinTimeout {
					t.Errorf("gRPC stream lifecycle evidence is incomplete: %#v\n%s", stream, output)
				}
				if test.grpcStreamPassed && (stream.Messages < 2 || stream.FinalCode != "OK" || !stream.ClosedGracefully) {
					t.Errorf("graceful gRPC stream evidence = %#v, want OK close\n%s", stream, output)
				}
				if !test.grpcStreamPassed && (stream.FinalCode != "UNAVAILABLE" || stream.ClosedGracefully) {
					t.Errorf("broken gRPC stream evidence = %#v, want UNAVAILABLE close\n%s", stream, output)
				}
			}
			if test.richTraffic && (report.Traffic.Successful != 4 || report.Traffic.Failed != 0) {
				t.Errorf("rich traffic summary = %#v, want four successful HTTP 409 responses\n%s", report.Traffic, output)
			}
			if test.commandTraffic && (report.Traffic.Successful != 4 || report.Traffic.Failed != 0) {
				t.Errorf("command traffic summary = %#v, want four successful command probes\n%s", report.Traffic, output)
			}
			if test.grpcTraffic && (report.Traffic.Successful != 4 || report.Traffic.Failed != 0) {
				t.Errorf("gRPC traffic summary = %#v, want four successful unary calls\n%s", report.Traffic, output)
			}
			if test.mode == "grpc-multiport" {
				assertMappedContainerPorts(t, report, []int{8080, 50051}, output)
			}
			if test.mode == "grpc-readiness" {
				assertGRPCReadinessEvidence(t, report, output)
			}
			assertJUnit(t, junitPath, report)
			bundle := assertDebugBundle(t, debugPath, report)
			if test.missingSignalLog {
				assertApplicationDidNotReceiveSignal(t, bundle["container.log"], output)
			}
			assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
		})
	}

	t.Run("repeated graceful lifecycle", func(t *testing.T) {
		runRepeatConformance(t, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image)
	})

	t.Run("scenario suite", func(t *testing.T) {
		runSuiteConformance(t, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image)
	})

	t.Run("interrupt cleanup", func(t *testing.T) {
		if goruntime.GOOS == "windows" {
			t.Skip("console interrupt delivery is covered on Linux CI runners")
		}
		runInterruptionConformance(t, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image)
	})

	t.Run("external container removal", func(t *testing.T) {
		runContainerDisappearanceConformance(t, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image)
	})
}

func runSuiteConformance(t *testing.T, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image string) {
	t.Helper()
	neverReadyPath := filepath.Join(temporary, "suite-never-ready.yaml")
	gracefulPath := filepath.Join(temporary, "suite-graceful.yaml")
	if err := os.WriteFile(neverReadyPath, []byte(conformanceConfig("never-ready")), 0o600); err != nil {
		t.Fatalf("write failing suite configuration: %v", err)
	}
	if err := os.WriteFile(gracefulPath, []byte(conformanceConfig("graceful")), 0o600); err != nil {
		t.Fatalf("write passing suite configuration: %v", err)
	}
	suiteDirectory := filepath.Join(reportDirectory, "suite")
	command := exec.Command(
		draincheckBinary,
		"suite", image,
		"--config", neverReadyPath,
		"--config", gracefulPath,
		"--runtime", runtimeName,
		"--report-dir", suiteDirectory,
		"--no-color",
	)
	command.Dir = root
	output, runErr := command.CombinedOutput()
	if code := exitCode(runErr); code != 1 {
		t.Fatalf("suite exit code = %d, want 1\n%s", code, output)
	}
	if !bytes.Contains(output, []byte("SUITE FAIL")) || !bytes.Contains(output, []byte("1/2 scenarios passed")) {
		t.Errorf("suite output does not contain the aggregate failure:\n%s", output)
	}

	data, err := os.ReadFile(filepath.Join(suiteDirectory, "summary.json"))
	if err != nil {
		t.Fatalf("read suite summary: %v", err)
	}
	var summary suiteConformanceSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode suite summary: %v", err)
	}
	if summary.SchemaVersion != 1 || summary.Passed || summary.ScenariosRequested != 2 || summary.ScenariosCompleted != 2 || summary.ScenariosPassed != 1 || summary.ScenariosFailed != 1 || summary.ExecutionErrors != 0 {
		t.Fatalf("unexpected suite summary: %#v\n%s", summary, output)
	}
	if len(summary.Scenarios) != 2 || summary.Scenarios[0].Name != "suite-never-ready" || summary.Scenarios[0].Passed || summary.Scenarios[1].Name != "suite-graceful" || !summary.Scenarios[1].Passed {
		t.Fatalf("suite did not continue in declared scenario order: %#v\n%s", summary.Scenarios, output)
	}
	if len(summary.Scenarios[0].FailedAssertions) != 1 || summary.Scenarios[0].FailedAssertions[0] != "startup.ready" {
		t.Errorf("failing suite scenario assertions = %v, want startup.ready", summary.Scenarios[0].FailedAssertions)
	}
	assertSuiteJUnit(t, filepath.Join(suiteDirectory, "summary.xml"), 2, 1, 0, 0)
	for _, scenario := range summary.Scenarios {
		artifactDirectory := filepath.Join(suiteDirectory, filepath.FromSlash(scenario.ArtifactDirectory))
		report := readReport(t, filepath.Join(artifactDirectory, "draincheck.json"))
		if report.RunID != scenario.RunID || report.Passed != scenario.Passed {
			t.Errorf("suite scenario report = %q/%t, want %q/%t", report.RunID, report.Passed, scenario.RunID, scenario.Passed)
		}
		assertJUnit(t, filepath.Join(artifactDirectory, "draincheck.xml"), report)
		assertDebugBundle(t, filepath.Join(artifactDirectory, "draincheck-debug.zip"), report)
		assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
	}
}

func runRepeatConformance(t *testing.T, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image string) {
	t.Helper()
	configPath := filepath.Join(temporary, "repeat.yaml")
	passingConfig := conformanceConfig("graceful") + `
repeat:
  budgets:
    startup_ready_p95: 30s
    readiness_withdrawal_p95: 30s
    container_exit_p95: 30s
`
	if err := os.WriteFile(configPath, []byte(passingConfig), 0o600); err != nil {
		t.Fatalf("write repeat configuration: %v", err)
	}
	repeatDirectory := filepath.Join(reportDirectory, "repeat")
	command := exec.Command(
		draincheckBinary,
		"repeat", image,
		"--runs", "2",
		"--runtime", runtimeName,
		"--config", configPath,
		"--report-dir", repeatDirectory,
		"--no-color",
	)
	command.Dir = root
	output, runErr := command.CombinedOutput()
	if code := exitCode(runErr); code != 0 {
		t.Fatalf("repeat exit code = %d, want 0\n%s", code, output)
	}
	if !bytes.Contains(output, []byte("REPEAT PASS")) {
		t.Errorf("repeat output has no passing summary:\n%s", output)
	}

	data, err := os.ReadFile(filepath.Join(repeatDirectory, "summary.json"))
	if err != nil {
		t.Fatalf("read repeat summary: %v", err)
	}
	var summary repeatConformanceSummary
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode repeat summary: %v", err)
	}
	if summary.SchemaVersion != 1 || !summary.Passed || summary.RunsRequested != 2 || summary.RunsCompleted != 2 || summary.RunsPassed != 2 {
		t.Fatalf("unexpected repeat summary: %#v\n%s", summary, output)
	}
	if len(summary.Runs) != 2 || summary.Timings.Verification.Samples != 2 {
		t.Fatalf("repeat run/timing evidence = %d/%d, want 2/2", len(summary.Runs), summary.Timings.Verification.Samples)
	}
	if summary.BudgetFailures != 0 || len(summary.BudgetAssertions) != 3 {
		t.Fatalf("repeat budget evidence = %d/%#v, want 0 failures and 3 assertions", summary.BudgetFailures, summary.BudgetAssertions)
	}
	for _, assertion := range summary.BudgetAssertions {
		if !assertion.Evaluated || !assertion.Passed || assertion.Samples != 2 || assertion.ObservedP95MS > assertion.LimitMS {
			t.Errorf("passing repeat budget assertion = %#v", assertion)
		}
	}
	assertRepeatJUnit(t, filepath.Join(repeatDirectory, "summary.xml"), 5, 0)
	for _, run := range summary.Runs {
		artifactDirectory := filepath.Join(repeatDirectory, filepath.FromSlash(run.ArtifactDirectory))
		reportPath := filepath.Join(artifactDirectory, "draincheck.json")
		report := readReport(t, reportPath)
		if report.RunID != run.RunID || !report.Passed {
			t.Errorf("repeat run report = %q/%t, want %q/true", report.RunID, report.Passed, run.RunID)
		}
		if report.Timings.StartupReadyMS <= 0 || report.Timings.ReadinessWithdrawalMS <= 0 || report.Timings.ContainerExitMS <= 0 {
			t.Errorf("repeat run has incomplete timing evidence: %#v", report.Timings)
		}
		assertJUnit(t, filepath.Join(artifactDirectory, "draincheck.xml"), report)
		assertDebugBundle(t, filepath.Join(artifactDirectory, "draincheck-debug.zip"), report)
		assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
	}

	failedConfigPath := filepath.Join(temporary, "repeat-failing.yaml")
	if err := os.WriteFile(failedConfigPath, []byte(conformanceConfig("never-ready")), 0o600); err != nil {
		t.Fatalf("write failing repeat configuration: %v", err)
	}
	failedDirectory := filepath.Join(reportDirectory, "repeat-failing")
	failedCommand := exec.Command(
		draincheckBinary,
		"repeat", image,
		"--runs", "2",
		"--runtime", runtimeName,
		"--config", failedConfigPath,
		"--report-dir", failedDirectory,
		"--no-color",
	)
	failedCommand.Dir = root
	failedOutput, failedRunErr := failedCommand.CombinedOutput()
	if code := exitCode(failedRunErr); code != 1 {
		t.Fatalf("failing repeat exit code = %d, want 1\n%s", code, failedOutput)
	}
	failedData, err := os.ReadFile(filepath.Join(failedDirectory, "summary.json"))
	if err != nil {
		t.Fatalf("read failing repeat summary: %v", err)
	}
	var failedSummary repeatConformanceSummary
	if err := json.Unmarshal(failedData, &failedSummary); err != nil {
		t.Fatalf("decode failing repeat summary: %v", err)
	}
	if failedSummary.Passed || failedSummary.RunsCompleted != 2 || failedSummary.RunsPassed != 0 || failedSummary.RunsFailed != 2 {
		t.Fatalf("unexpected failing repeat summary: %#v\n%s", failedSummary, failedOutput)
	}
	assertRepeatJUnit(t, filepath.Join(failedDirectory, "summary.xml"), 2, 2)
	for _, run := range failedSummary.Runs {
		artifactDirectory := filepath.Join(failedDirectory, filepath.FromSlash(run.ArtifactDirectory))
		report := readReport(t, filepath.Join(artifactDirectory, "draincheck.json"))
		assertFailedAssertions(t, report, []string{"startup.ready"}, failedOutput)
		assertDebugBundle(t, filepath.Join(artifactDirectory, "draincheck-debug.zip"), report)
		assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
	}

	budgetConfigPath := filepath.Join(temporary, "repeat-budget-failing.yaml")
	budgetConfig := conformanceConfig("graceful") + `
repeat:
  budgets:
    startup_ready_p95: 1ms
`
	if err := os.WriteFile(budgetConfigPath, []byte(budgetConfig), 0o600); err != nil {
		t.Fatalf("write budget-failing repeat configuration: %v", err)
	}
	budgetDirectory := filepath.Join(reportDirectory, "repeat-budget-failing")
	budgetCommand := exec.Command(
		draincheckBinary,
		"repeat", image,
		"--runs", "2",
		"--runtime", runtimeName,
		"--config", budgetConfigPath,
		"--report-dir", budgetDirectory,
		"--no-color",
	)
	budgetCommand.Dir = root
	budgetOutput, budgetRunErr := budgetCommand.CombinedOutput()
	if code := exitCode(budgetRunErr); code != 1 {
		t.Fatalf("budget-failing repeat exit code = %d, want 1\n%s", code, budgetOutput)
	}
	if !bytes.Contains(budgetOutput, []byte("2/2 runs passed")) || !bytes.Contains(budgetOutput, []byte("FAIL repeat.startup_ready_p95")) {
		t.Errorf("budget-failing output does not distinguish green runs from the failed gate:\n%s", budgetOutput)
	}
	budgetData, err := os.ReadFile(filepath.Join(budgetDirectory, "summary.json"))
	if err != nil {
		t.Fatalf("read budget-failing repeat summary: %v", err)
	}
	var budgetSummary repeatConformanceSummary
	if err := json.Unmarshal(budgetData, &budgetSummary); err != nil {
		t.Fatalf("decode budget-failing repeat summary: %v", err)
	}
	if budgetSummary.Passed || budgetSummary.RunsPassed != 2 || budgetSummary.RunsFailed != 0 || budgetSummary.BudgetFailures != 1 || len(budgetSummary.BudgetAssertions) != 1 {
		t.Fatalf("unexpected budget-failing repeat summary: %#v\n%s", budgetSummary, budgetOutput)
	}
	budgetAssertion := budgetSummary.BudgetAssertions[0]
	if budgetAssertion.Name != "repeat.startup_ready_p95" || !budgetAssertion.Evaluated || budgetAssertion.Passed || budgetAssertion.Samples != 2 || budgetAssertion.ObservedP95MS <= budgetAssertion.LimitMS {
		t.Fatalf("budget-failing assertion = %#v\n%s", budgetAssertion, budgetOutput)
	}
	assertRepeatJUnit(t, filepath.Join(budgetDirectory, "summary.xml"), 3, 1)
	for _, run := range budgetSummary.Runs {
		artifactDirectory := filepath.Join(budgetDirectory, filepath.FromSlash(run.ArtifactDirectory))
		report := readReport(t, filepath.Join(artifactDirectory, "draincheck.json"))
		if !report.Passed {
			t.Errorf("budget-gated repeat altered per-run verdict: %#v", report.Assertions)
		}
		assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
	}
}

func assertRepeatJUnit(t *testing.T, path string, tests, failures int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read repeat JUnit summary: %v", err)
	}
	var suite conformanceJUnit
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatalf("decode repeat JUnit summary: %v", err)
	}
	if suite.XMLName.Local != "testsuite" || suite.Tests != tests || suite.Failures != failures {
		t.Errorf("repeat JUnit summary = root %q, tests %d, failures %d; want tests %d, failures %d",
			suite.XMLName.Local, suite.Tests, suite.Failures, tests, failures)
	}
}

func assertSuiteJUnit(t *testing.T, path string, tests, failures, executionErrors, skipped int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read suite JUnit summary: %v", err)
	}
	var suite conformanceJUnit
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatalf("decode suite JUnit summary: %v", err)
	}
	if suite.XMLName.Local != "testsuite" || suite.Tests != tests || suite.Failures != failures || suite.Errors != executionErrors || suite.Skipped != skipped {
		t.Errorf("suite JUnit summary = root %q, tests %d, failures %d, errors %d, skipped %d; want tests %d, failures %d, errors %d, skipped %d",
			suite.XMLName.Local, suite.Tests, suite.Failures, suite.Errors, suite.Skipped, tests, failures, executionErrors, skipped)
	}
}

func runInterruptionConformance(t *testing.T, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image string) {
	t.Helper()
	configPath := filepath.Join(temporary, "interrupted.yaml")
	config := strings.Replace(conformanceConfig("never-ready"), "startup_timeout: 2s", "startup_timeout: 30s", 1)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write interruption configuration: %v", err)
	}
	jsonPath := filepath.Join(reportDirectory, "interrupted.json")
	junitPath := filepath.Join(reportDirectory, "interrupted.xml")
	debugPath := filepath.Join(reportDirectory, "interrupted-debug.zip")

	command := exec.Command(
		draincheckBinary,
		"verify", image,
		"--runtime", runtimeName,
		"--config", configPath,
		"--report-json", jsonPath,
		"--report-junit", junitPath,
		"--debug-bundle", debugPath,
		"--no-color",
	)
	command.Dir = root
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start interrupted verification: %v", err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished && command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
		removeContainersForImage(t, runtimeBinary, runtimeName, image)
	})

	if _, err := waitForRunningContainer(runtimeBinary, image, 20*time.Second); err != nil {
		t.Fatalf("wait for interrupt target: %v\n%s", err, output.String())
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupt Draincheck: %v", err)
	}
	runErr := command.Wait()
	finished = true
	if code := exitCode(runErr); code != 130 {
		t.Fatalf("interrupted exit code = %d, want 130\n%s", code, output.String())
	}

	report := readReport(t, jsonPath)
	if report.Passed {
		t.Fatalf("interrupted report unexpectedly passed\n%s", output.String())
	}
	assertFailedAssertions(t, report, []string{"execution.completed"}, output.Bytes())
	assertJUnit(t, junitPath, report)
	assertDebugBundle(t, debugPath, report)
	assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
}

func runContainerDisappearanceConformance(t *testing.T, root, temporary, reportDirectory, draincheckBinary, runtimeBinary, runtimeName, image string) {
	t.Helper()
	configPath := filepath.Join(temporary, "container-disappeared.yaml")
	config := strings.Replace(conformanceConfig("never-ready"), "startup_timeout: 2s", "startup_timeout: 30s", 1)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write disappearance configuration: %v", err)
	}
	jsonPath := filepath.Join(reportDirectory, "container-disappeared.json")
	junitPath := filepath.Join(reportDirectory, "container-disappeared.xml")
	debugPath := filepath.Join(reportDirectory, "container-disappeared-debug.zip")

	command := exec.Command(
		draincheckBinary,
		"verify", image,
		"--runtime", runtimeName,
		"--config", configPath,
		"--report-json", jsonPath,
		"--report-junit", junitPath,
		"--debug-bundle", debugPath,
		"--no-color",
	)
	command.Dir = root
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start disappearance verification: %v", err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished && command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
		removeContainersForImage(t, runtimeBinary, runtimeName, image)
	})

	ids, err := waitForRunningContainer(runtimeBinary, image, 20*time.Second)
	if err != nil {
		t.Fatalf("wait for disappearance target: %v\n%s", err, output.String())
	}
	if cleanupOutput, cleanupErr := forceRemoveContainers(runtimeBinary, runtimeName, ids); cleanupErr != nil {
		t.Fatalf("remove container externally: %v: %s", cleanupErr, cleanupOutput)
	}
	runErr := command.Wait()
	finished = true
	if code := exitCode(runErr); code != 3 {
		t.Fatalf("container disappearance exit code = %d, want 3\n%s", code, output.String())
	}

	report := readReport(t, jsonPath)
	if report.Passed {
		t.Fatalf("container disappearance report unexpectedly passed\n%s", output.String())
	}
	assertFailedAssertions(t, report, []string{"execution.completed"}, output.Bytes())
	assertJUnit(t, junitPath, report)
	assertDebugBundle(t, debugPath, report)
	assertNoContainers(t, runtimeBinary, runtimeName, report.RunID)
}

func waitForRunningContainer(runtimeBinary, image string, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		command := exec.Command(runtimeBinary, "ps", "-q", "--filter", "label=io.draincheck.run", "--filter", "ancestor="+image)
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("inspect conformance target: %w: %s", err, output)
		}
		ids := strings.Fields(string(output))
		if len(ids) > 0 {
			return ids, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("no running Draincheck container appeared within %s", timeout)
}

func removeContainersForImage(t *testing.T, runtimeBinary, runtimeName, image string) {
	t.Helper()
	query := exec.Command(runtimeBinary, "ps", "-aq", "--filter", "label=io.draincheck.run", "--filter", "ancestor="+image)
	output, err := query.CombinedOutput()
	if err != nil {
		t.Logf("inspect interruption cleanup: %v: %s", err, output)
		return
	}
	ids := strings.Fields(string(output))
	if len(ids) == 0 {
		return
	}
	if cleanupOutput, cleanupErr := forceRemoveContainers(runtimeBinary, runtimeName, ids); cleanupErr != nil {
		t.Logf("remove interruption containers: %v: %s", cleanupErr, cleanupOutput)
	}
}

func forceRemoveContainers(runtimeBinary, runtimeName string, ids []string) ([]byte, error) {
	args := []string{"rm", "--force"}
	if runtimeName == "podman" {
		args = append(args, "--time", "0")
	}
	args = append(args, ids...)
	return exec.Command(runtimeBinary, args...).CombinedOutput()
}

func assertSignalEvidence(t *testing.T, report conformanceReport, requireConfirmedTraffic bool, output []byte) {
	t.Helper()
	requestedIndex := -1
	confirmedIndex := -1
	for index, event := range report.Events {
		if event.Phase != "terminating" {
			continue
		}
		if strings.Contains(event.Message, "requested") {
			requestedIndex = index
		}
		if strings.Contains(event.Message, "delivery confirmed") {
			confirmedIndex = index
		}
	}
	if requestedIndex < 0 || confirmedIndex <= requestedIndex {
		t.Errorf("signal request/confirmation events missing or out of order: %#v\n%s", report.Events, output)
	}
	if requireConfirmedTraffic && report.Traffic.Inflight < 1 {
		t.Errorf("no requests remained active at signal confirmation\n%s", output)
	}
}

func conformanceConfig(mode string) string {
	return conformanceConfigForCase(mode, "disabled", false, false, false, false, false, false, false, false, "")
}

func conformanceConfigWithPostSignal(mode, policy string) string {
	return conformanceConfigForCase(mode, policy, false, false, false, false, false, false, false, false, "")
}

func conformanceConfigForCase(mode, policy string, telemetryEnabled, metricsEnabled, streamingEnabled, webSocketEnabled, richTraffic, commandTraffic, grpcTraffic, grpcStreaming bool, probeBinaryName string) string {
	if policy == "" {
		policy = "disabled"
	}
	// Leave headroom after the 2s work completes for the runtime to publish the final exit state.
	// Busy CI runners have taken more than 3s even after the fixture logged a graceful shutdown.
	shutdownDeadline := "5s"
	if policy != "disabled" || telemetryEnabled || metricsEnabled || streamingEnabled || webSocketEnabled || grpcStreaming {
		shutdownDeadline = "4s"
	}
	method := "GET"
	requestOptions := "    headers: {}\n"
	if richTraffic {
		method = "POST"
		requestOptions = "    headers:\n      Content-Type: application/json\n    body_file: rich-request.json\n    success_statuses: [409]\n"
	}
	driverOptions := "  driver: http\n"
	if commandTraffic {
		driverOptions = fmt.Sprintf("  driver: command\n  command:\n    executable: ./%s\n    args: []\n    environment: {}\n    working_directory: .\n", probeBinaryName)
	} else if grpcTraffic {
		driverOptions = "  driver: grpc\n"
		if mode == "grpc-multiport" {
			driverOptions += "  container_port: 50051\n"
		}
		driverOptions += "  grpc:\n    method: grpc.health.v1.Health/Check\n    request: '{\"service\":\"slow\"}'\n    metadata:\n      x-draincheck: conformance\n    expected_codes: [OK]\n"
	}
	grpcStreamPort := ""
	if mode == "grpc-multiport" {
		grpcStreamPort = "    container_port: 50051\n"
	}
	readinessDriver := "  driver: http\n"
	readinessWithdrawal := "800ms"
	preStopOptions := ""
	shutdownDeadlineOption := "  deadline: " + shutdownDeadline + "\n"
	if mode == "grpc-readiness" {
		readinessDriver = "  driver: grpc\n  grpc:\n    service: ready\n"
	} else if mode == "exec-readiness" {
		readinessDriver = "  driver: exec\n  exec:\n    command: [\"/fixture\", \"healthcheck\"]\n"
		readinessWithdrawal = "2s"
	}
	if mode == "kubernetes-prestop" {
		preStopOptions = "  pre_stop:\n    exec:\n      command: [\"/fixture\", \"prestop\"]\n"
		shutdownDeadlineOption = ""
	} else if mode == "kubernetes-prestop-fail" {
		preStopOptions = "  pre_stop:\n    exec:\n      command: [\"/fixture\", \"prestop-fail\"]\n"
		shutdownDeadlineOption = ""
	}
	return fmt.Sprintf(`version: 1

target:
  image: overridden-by-test
  container_port: 8080
  environment:
    DRAINCHECK_FIXTURE_MODE: %s

readiness:
%s  path: /ready
  success_status: 200
  startup_timeout: 2s
  interval: 50ms

traffic:
%s
  request:
    method: %s
    path: /work?delay=2s
%s  count: 4
  concurrency: 4
  shutdown_after: 150ms
  request_timeout: 5s
  post_signal:
    policy: %s
    delay: 250ms
    count: 2

streaming:
  sse:
    enabled: %t
    path: /events
    initial_event: ready
    terminal_event: shutdown
    establish_timeout: 1s
    close_timeout: 1s
  websocket:
    enabled: %t
    path: /ws
    headers: {}
    subprotocols: []
    terminal_message: shutdown
    close_code: 1000
    establish_timeout: 1s
    close_timeout: 1s
  grpc:
    enabled: %t
%s    method: grpc.health.v1.Health/Watch
    request: '{"service":"stream"}'
    metadata:
      x-draincheck: conformance
    minimum_messages: 1
    expected_code: OK
    establish_timeout: 1s
    close_timeout: 3s

telemetry:
  traces:
    enabled: %t
    minimum_correlated_spans: 1
    flush_timeout: 750ms
  metrics:
    enabled: %t
    minimum_data_points: 1
    flush_timeout: 750ms

shutdown:
  signal: SIGTERM
%s%s

assertions:
  readiness_withdrawn_within: %s
  inflight_requests_complete: true
  max_failed_requests: 0
  exit_code: 0
  forbid_force_kill: true
`, mode, readinessDriver, driverOptions, method, requestOptions, policy, streamingEnabled, webSocketEnabled, grpcStreaming, grpcStreamPort, telemetryEnabled, metricsEnabled, shutdownDeadlineOption, preStopOptions, readinessWithdrawal)
}

func assertMappedContainerPorts(t *testing.T, report conformanceReport, expected []int, output []byte) {
	t.Helper()
	found := make(map[int]bool)
	for _, event := range report.Events {
		for _, port := range expected {
			if strings.Contains(event.Message, fmt.Sprintf("mapped container port %d ", port)) {
				found[port] = true
			}
		}
	}
	for _, port := range expected {
		if !found[port] {
			t.Errorf("no mapping event for container port %d\n%s", port, output)
		}
	}
}

func assertGRPCReadinessEvidence(t *testing.T, report conformanceReport, output []byte) {
	t.Helper()
	expected := map[string]string{
		"startup.ready":       "gRPC SERVING",
		"readiness.withdrawn": "gRPC ",
	}
	for _, assertion := range report.Assertions {
		fragment, found := expected[assertion.Name]
		if !found {
			continue
		}
		if !assertion.Passed || !strings.Contains(assertion.Message, fragment) {
			t.Errorf("gRPC readiness assertion %q = %#v, want message containing %q\n%s", assertion.Name, assertion, fragment, output)
		}
		delete(expected, assertion.Name)
	}
	if len(expected) != 0 {
		t.Errorf("missing gRPC readiness assertions %v\n%s", expected, output)
	}
}

func assertExecReadinessEvidence(t *testing.T, report conformanceReport, output []byte) {
	t.Helper()
	expected := map[string]string{
		"startup.ready":       "exec exit 0",
		"readiness.withdrawn": "exec exit 1",
	}
	for _, assertion := range report.Assertions {
		fragment, found := expected[assertion.Name]
		if !found {
			continue
		}
		if !assertion.Passed || !strings.Contains(assertion.Message, fragment) {
			t.Errorf("exec readiness assertion %q = %#v, want message containing %q\n%s", assertion.Name, assertion, fragment, output)
		}
		delete(expected, assertion.Name)
	}
	if len(expected) != 0 {
		t.Errorf("missing exec readiness assertions %v\n%s", expected, output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate conformance test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func readReport(t *testing.T, path string) conformanceReport {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JSON report: %v", err)
	}
	var report conformanceReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode JSON report: %v", err)
	}
	if report.RunID == "" {
		t.Fatal("JSON report has an empty run ID")
	}
	return report
}

func assertFailedAssertions(t *testing.T, report conformanceReport, expected []string, output []byte) {
	t.Helper()
	actual := make(map[string]bool)
	for _, assertion := range report.Assertions {
		if !assertion.Passed {
			actual[assertion.Name] = true
		}
	}
	if len(expected) == 0 && len(actual) != 0 {
		t.Errorf("unexpected failed assertions: %v\n%s", sortedKeys(actual), output)
	}
	for _, name := range expected {
		if !actual[name] {
			t.Errorf("missing failed assertion %q; got %v\n%s", name, sortedKeys(actual), output)
		}
	}
}

func assertJUnit(t *testing.T, path string, report conformanceReport) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read JUnit report: %v", err)
	}
	var suite conformanceJUnit
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatalf("decode JUnit report: %v", err)
	}
	failed := 0
	for _, assertion := range report.Assertions {
		if !assertion.Passed {
			failed++
		}
	}
	if suite.XMLName.Local != "testsuite" || suite.Tests != len(report.Assertions) || suite.Failures != failed {
		t.Errorf("JUnit summary = root %q, tests %d, failures %d; want tests %d, failures %d",
			suite.XMLName.Local, suite.Tests, suite.Failures, len(report.Assertions), failed)
	}
}

func assertDebugBundle(t *testing.T, path string, report conformanceReport) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open debug bundle: %v", err)
	}
	defer archive.Close()

	entries := make(map[string][]byte, len(archive.File))
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open debug bundle entry %q: %v", file.Name, err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatalf("read debug bundle entry %q: %v", file.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close debug bundle entry %q: %v", file.Name, closeErr)
		}
		entries[file.Name] = data
	}
	for _, name := range []string{"config.json", "timeline.json", "runtime-state.json", "container.log"} {
		if _, found := entries[name]; !found {
			t.Errorf("debug bundle is missing %q", name)
		}
	}
	for _, name := range []string{"config.json", "runtime-state.json"} {
		var decoded any
		if err := json.Unmarshal(entries[name], &decoded); err != nil {
			t.Errorf("decode debug bundle entry %q: %v", name, err)
		}
	}
	var timeline struct {
		SchemaVersion int    `json:"schema_version"`
		RunID         string `json:"run_id"`
	}
	if err := json.Unmarshal(entries["timeline.json"], &timeline); err != nil {
		t.Errorf("decode debug timeline: %v", err)
	} else if timeline.SchemaVersion != 1 || timeline.RunID != report.RunID {
		t.Errorf("debug timeline schema/run = %d/%q, want 1/%q", timeline.SchemaVersion, timeline.RunID, report.RunID)
	}
	return entries
}

func assertApplicationDidNotReceiveSignal(t *testing.T, containerLog, output []byte) {
	t.Helper()
	if !bytes.Contains(containerLog, []byte("fixture listening")) {
		t.Errorf("non-forwarding fixture log does not prove the child service started: %q\n%s", containerLog, output)
	}
	if bytes.Contains(containerLog, []byte("received terminated")) {
		t.Errorf("child application unexpectedly received SIGTERM: %q\n%s", containerLog, output)
	}
}

func assertNoContainers(t *testing.T, runtimeBinary, runtimeName, runID string) {
	t.Helper()
	command := exec.Command(runtimeBinary, "ps", "-aq", "--filter", "label=io.draincheck.run="+runID)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect cleanup: %v\n%s", err, output)
	}
	ids := strings.Fields(string(output))
	if len(ids) == 0 {
		return
	}
	args := []string{"rm", "--force"}
	if runtimeName == "podman" {
		args = append(args, "--time", "0")
	}
	args = append(args, ids...)
	cleanup := exec.Command(runtimeBinary, args...)
	cleanupOutput, cleanupErr := cleanup.CombinedOutput()
	t.Errorf("Draincheck left containers behind: %v (cleanup: %v, %s)", ids, cleanupErr, cleanupOutput)
}

func runCommand(t *testing.T, description, directory, name string, args ...string) {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", description, err, output)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func environment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
