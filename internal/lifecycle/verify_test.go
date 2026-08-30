package lifecycle

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/ssubedir/draincheck/internal/config"
	"github.com/ssubedir/draincheck/internal/readiness"
	"github.com/ssubedir/draincheck/internal/report"
	containerruntime "github.com/ssubedir/draincheck/internal/runtime"
	"github.com/ssubedir/draincheck/internal/traffic"
)

func TestCompletedInflightBoundaryUsesLatestEligibleCompletion(t *testing.T) {
	signalAt := time.Now()
	results := []traffic.Result{
		{ID: 1, StartedAt: signalAt.Add(-time.Second), Duration: 1500 * time.Millisecond},
		{ID: 2, StartedAt: signalAt.Add(-time.Second), Duration: 2 * time.Second},
		{ID: 3, StartedAt: signalAt.Add(-time.Second), Duration: 3 * time.Second},
	}
	got := completedInflightBoundary(signalAt, []int{1, 2}, results)
	want := signalAt.Add(time.Second)
	if !got.Equal(want) {
		t.Fatalf("boundary = %s, want %s", got, want)
	}
}

func TestTrafficFailureMessageIncludesBoundedStatusEvidence(t *testing.T) {
	message := trafficFailureMessage([]traffic.Result{
		{Status: http.StatusOK, ErrorKind: "http_status"},
		{Status: http.StatusOK, ErrorKind: "http_status"},
		{ErrorKind: "timeout"},
		{GRPCCode: "UNAVAILABLE", ErrorKind: "grpc_status"},
		{Success: true, Status: http.StatusAccepted},
	}, 4, 0)
	for _, expected := range []string{"4 failed requests", "HTTP 200=2", "timeout=1", "gRPC UNAVAILABLE=1"} {
		if !strings.Contains(message, expected) {
			t.Errorf("message %q does not contain %q", message, expected)
		}
	}
}

func TestCommandProtocolFailureDoesNotSatisfyPostSignalRejection(t *testing.T) {
	cfg := lifecycleTestConfig()
	cfg.Traffic.Driver = config.TrafficDriverCommand
	cfg.Traffic.PostSignal.Policy = config.PostSignalReject
	cfg.Traffic.PostSignal.Count = 1
	result := report.New("run", cfg.Target.Image, "fixture", time.Now())
	result.Traffic.PostSignal.Configured = 1
	verifier := verifier{config: cfg, report: result}

	verifier.summarizePostSignalTraffic([]traffic.Result{{ID: 1, ErrorKind: "protocol_json"}})
	verifier.evaluatePostSignalAssertion()

	if result.Traffic.PostSignal.Completed != 1 || result.Traffic.PostSignal.Rejected != 0 {
		t.Fatalf("post-signal summary = %#v, want one invalid result and no rejection", result.Traffic.PostSignal)
	}
	failed := result.FailedAssertions()
	if len(failed) != 1 || failed[0].Name != "traffic.post_signal_policy" {
		t.Fatalf("failed assertions = %#v, want traffic.post_signal_policy", failed)
	}
}

func TestVerifyPassesGracefulLifecycle(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	cfg := lifecycleTestConfig()

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	if !runtime.removed {
		t.Fatal("container was not cleaned up")
	}
	if result.Traffic.Inflight == 0 || result.Traffic.InflightBad != 0 {
		t.Fatalf("traffic summary = %#v", result.Traffic)
	}
	if runtime.waitCalls != 1 {
		t.Fatalf("runtime wait calls = %d, want 1", runtime.waitCalls)
	}
}

func TestVerifyRunsPreStopBeforeSignalWithinSharedDeadline(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.preStopDelay = 20 * time.Millisecond
	cfg := lifecycleTestConfig()
	cfg.Shutdown.PreStop = &config.PreStopHook{Exec: config.ExecHook{Command: []string{"/prestop", "--drain"}}}

	result, err := Verify(context.Background(), cfg, runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
		Profile:    config.ProfileKubernetes,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	if result.Profile != "kubernetes" || result.Shutdown.DeadlineMS != 1000 {
		t.Fatalf("profile/shutdown summary = %q/%#v", result.Profile, result.Shutdown)
	}
	if !result.Shutdown.PreStop.Configured || result.Shutdown.PreStop.ExitCode != 0 || result.Shutdown.PreStop.TimedOut || result.Shutdown.PreStop.DurationMS < 15 {
		t.Fatalf("pre-stop summary = %#v", result.Shutdown.PreStop)
	}
	if runtime.preStopCompletedAt.IsZero() || runtime.signalAt.IsZero() || runtime.signalAt.Before(runtime.preStopCompletedAt) {
		t.Fatalf("pre-stop completed at %s, signal requested at %s", runtime.preStopCompletedAt, runtime.signalAt)
	}
	if result.Timings.PreStopMS < 15 || result.Timings.ShutdownTotalMS < result.Timings.ContainerExitMS {
		t.Fatalf("timings = %#v", result.Timings)
	}
}

func TestVerifyReportsFailedPreStopAndStillSignals(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.preStopExitCode = 7
	cfg := lifecycleTestConfig()
	cfg.Shutdown.PreStop = &config.PreStopHook{Exec: config.ExecHook{Command: []string{"/prestop"}}}

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024, Profile: config.ProfileKubernetes})
	if err != nil {
		t.Fatal(err)
	}
	failed := result.FailedAssertions()
	if len(failed) != 1 || failed[0].Name != "shutdown.pre_stop" || !strings.Contains(failed[0].Message, "code 7") {
		t.Fatalf("failed assertions = %#v, want failed pre-stop only", failed)
	}
	if runtime.signalAt.IsZero() {
		t.Fatal("SIGTERM was not attempted after the failed pre-stop command")
	}
}

func TestPreStopTimeoutExhaustsSharedShutdownDeadline(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.preStopDelay = time.Second
	cfg := lifecycleTestConfig()
	cfg.Shutdown.Deadline = config.NewDuration(20 * time.Millisecond)
	cfg.Shutdown.PreStop = &config.PreStopHook{Exec: config.ExecHook{Command: []string{"/prestop"}}}
	result := report.New("run", cfg.Target.Image, runtime.Name(), time.Now())
	verifier := verifier{config: cfg, runtime: runtime, report: result}

	exhausted, err := verifier.runPreStop(context.Background(), "fixture", time.Now().Add(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if !exhausted || !result.Shutdown.PreStop.TimedOut {
		t.Fatalf("exhausted=%t, pre-stop=%#v", exhausted, result.Shutdown.PreStop)
	}
	failed := result.FailedAssertions()
	if len(failed) != 1 || failed[0].Name != "shutdown.pre_stop" {
		t.Fatalf("failed assertions = %#v", failed)
	}
}

func TestVerifyPublishesAndResolvesEachSelectedProbePort(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	cfg := lifecycleTestConfig()
	cfg.Readiness.ContainerPort = lifecyclePortPointer(8081)
	cfg.Traffic.ContainerPort = lifecyclePortPointer(8082)
	cfg.Streaming.WebSocket.Enabled = true
	cfg.Streaming.WebSocket.ContainerPort = lifecyclePortPointer(8083)
	cfg.Streaming.WebSocket.EstablishTimeout = config.NewDuration(500 * time.Millisecond)
	cfg.Streaming.WebSocket.CloseTimeout = config.NewDuration(500 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	want := []int{8081, 8082, 8083}
	if !slices.Equal(runtime.createdSpec.ContainerPorts, want) {
		t.Fatalf("published container ports = %v, want %v", runtime.createdSpec.ContainerPorts, want)
	}
	if !slices.Equal(runtime.hostPortCalls, want) {
		t.Fatalf("resolved container ports = %v, want %v", runtime.hostPortCalls, want)
	}
}

func TestVerifyUsesExecReadinessWithoutPublishingItsPort(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	cfg := lifecycleTestConfig()
	cfg.Readiness.Driver = config.ReadinessDriverExec
	cfg.Readiness.Exec.Command = []string{"/app/healthcheck", "--ready"}
	cfg.Traffic.ContainerPort = lifecyclePortPointer(8082)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	if !slices.Equal(runtime.createdSpec.ContainerPorts, []int{8082}) || !slices.Equal(runtime.hostPortCalls, []int{8082}) {
		t.Fatalf("published ports = %v, resolved ports = %v, want only traffic port 8082", runtime.createdSpec.ContainerPorts, runtime.hostPortCalls)
	}
	if len(runtime.execCalls) < 2 {
		t.Fatalf("exec readiness calls = %d, want startup and withdrawal checks", len(runtime.execCalls))
	}
	for _, command := range runtime.execCalls {
		if !slices.Equal(command, cfg.Readiness.Exec.Command) {
			t.Fatalf("exec command = %v, want %v", command, cfg.Readiness.Exec.Command)
		}
	}
}

func TestVerifyReportsExecReadinessStartupTimeout(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.ready = false
	cfg := lifecycleTestConfig()
	cfg.Readiness.Driver = config.ReadinessDriverExec
	cfg.Readiness.Exec.Command = []string{"/app/healthcheck"}
	cfg.Readiness.StartupTimeout = config.NewDuration(40 * time.Millisecond)
	cfg.Readiness.Interval = config.NewDuration(10 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	failed := result.FailedAssertions()
	if len(failed) != 1 || failed[0].Name != "startup.ready" || !strings.Contains(failed[0].Message, "exec exit 1") {
		t.Fatalf("failed assertions = %#v, want exec startup readiness failure", failed)
	}
}

func lifecyclePortPointer(port int) *int { return &port }

func TestVerifyPassesGracefulWebSocketLifecycle(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	cfg := lifecycleTestConfig()
	cfg.Streaming.WebSocket.Enabled = true
	cfg.Streaming.WebSocket.EstablishTimeout = config.NewDuration(500 * time.Millisecond)
	cfg.Streaming.WebSocket.CloseTimeout = config.NewDuration(500 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	webSocket := result.Streaming.WebSocket
	if !webSocket.Established || !webSocket.ActiveAtSignal || !webSocket.TerminalMessageAfterSignal || !webSocket.CloseFrameReceived || webSocket.CloseCode != 1000 || !webSocket.ClosedGracefully {
		t.Fatalf("WebSocket summary = %#v", webSocket)
	}
}

func TestVerifyFailsWebSocketWithoutTerminalMessage(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.webSocketTerminal = false
	cfg := lifecycleTestConfig()
	cfg.Streaming.WebSocket.Enabled = true
	cfg.Streaming.WebSocket.EstablishTimeout = config.NewDuration(500 * time.Millisecond)
	cfg.Streaming.WebSocket.CloseTimeout = config.NewDuration(500 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["websocket.closed_gracefully"] || failed["websocket.active_at_signal"] {
		t.Fatalf("failed assertions = %#v", result.FailedAssertions())
	}
}

func TestVerifyFailsWebSocketWithUnexpectedCloseCode(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.webSocketCloseCode = websocket.StatusGoingAway
	cfg := lifecycleTestConfig()
	cfg.Streaming.WebSocket.Enabled = true
	cfg.Streaming.WebSocket.EstablishTimeout = config.NewDuration(500 * time.Millisecond)
	cfg.Streaming.WebSocket.CloseTimeout = config.NewDuration(500 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	webSocket := result.Streaming.WebSocket
	if webSocket.CloseCode != int(websocket.StatusGoingAway) || webSocket.ClosedGracefully {
		t.Fatalf("WebSocket summary = %#v", webSocket)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["websocket.closed_gracefully"] {
		t.Fatalf("failed assertions = %#v", result.FailedAssertions())
	}
}

func TestVerifyAcceptsPostSignalTrafficWhenRequired(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	cfg := lifecycleTestConfig()
	cfg.Traffic.PostSignal.Policy = config.PostSignalAccept
	cfg.Traffic.PostSignal.Count = 2

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	postSignal := result.Traffic.PostSignal
	if postSignal.Policy != config.PostSignalAccept || postSignal.Configured != 2 || postSignal.Completed != 2 || postSignal.Accepted != 2 || postSignal.Rejected != 0 {
		t.Fatalf("post-signal traffic summary = %#v", postSignal)
	}
}

func TestVerifyRejectsPostSignalTrafficWhenRequired(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.rejectWorkWhenUnready = true
	cfg := lifecycleTestConfig()
	cfg.Traffic.PostSignal.Policy = config.PostSignalReject
	cfg.Traffic.PostSignal.Count = 2

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("expected pass, failures: %#v", result.FailedAssertions())
	}
	postSignal := result.Traffic.PostSignal
	if postSignal.Policy != config.PostSignalReject || postSignal.Configured != 2 || postSignal.Completed != 2 || postSignal.Accepted != 0 || postSignal.Rejected != 2 {
		t.Fatalf("post-signal traffic summary = %#v", postSignal)
	}
}

func TestVerifyFailsPostSignalPolicyMismatch(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.rejectWorkWhenUnready = true
	cfg := lifecycleTestConfig()
	cfg.Traffic.PostSignal.Policy = config.PostSignalAccept

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	failed := result.FailedAssertions()
	if len(failed) != 1 || failed[0].Name != "traffic.post_signal_policy" {
		t.Fatalf("failed assertions = %#v, want only traffic.post_signal_policy", failed)
	}
	if result.Traffic.Failed != 0 {
		t.Fatalf("in-flight failed requests = %d, want post-signal rejection reported separately", result.Traffic.Failed)
	}
}

func TestVerifyDoesNotStartPostSignalTrafficAfterShutdownBudget(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.signalDelay = 80 * time.Millisecond
	cfg := lifecycleTestConfig()
	cfg.Traffic.PostSignal.Policy = config.PostSignalAccept
	cfg.Traffic.PostSignal.Delay = config.NewDuration(30 * time.Millisecond)
	cfg.Shutdown.Deadline = config.NewDuration(100 * time.Millisecond)
	cfg.Assertions.ReadinessWithdrawnWithin = config.NewDuration(90 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if result.Traffic.PostSignal.Started != 0 || result.Traffic.PostSignal.Completed != 0 {
		t.Fatalf("post-signal traffic started after its shutdown window: %#v", result.Traffic.PostSignal)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["traffic.post_signal_policy"] || !failed["shutdown.deadline"] {
		t.Fatalf("failed assertions = %#v, want post-signal policy and shutdown deadline", result.FailedAssertions())
	}
	var skipped bool
	for _, event := range result.Events {
		skipped = skipped || event.Phase == "post_signal" && strings.Contains(event.Message, "could not start")
	}
	if !skipped {
		t.Fatalf("post-signal deadline event missing: %#v", result.Events)
	}
}

func TestVerifyFailsIgnoredSignal(t *testing.T) {
	runtime, server := newFakeRuntime(t, true)
	defer server.Close()
	cfg := lifecycleTestConfig()
	cfg.Shutdown.Deadline = config.NewDuration(120 * time.Millisecond)
	cfg.Assertions.ReadinessWithdrawnWithin = config.NewDuration(80 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{PullPolicy: containerruntime.PullNever, LogLimit: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("ignored signal unexpectedly passed")
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	for _, name := range []string{"readiness.withdrawn", "shutdown.deadline", "shutdown.force_kill"} {
		if !failed[name] {
			t.Errorf("missing failed assertion %s: %#v", name, result.FailedAssertions())
		}
	}
	if !runtime.removed {
		t.Fatal("failed container was not cleaned up")
	}
}

func TestVerifyCanRetainFailedContainer(t *testing.T) {
	runtime, server := newFakeRuntime(t, true)
	defer server.Close()
	cfg := lifecycleTestConfig()
	cfg.Shutdown.Deadline = config.NewDuration(80 * time.Millisecond)
	cfg.Assertions.ReadinessWithdrawnWithin = config.NewDuration(50 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{
		PullPolicy:    containerruntime.PullNever,
		KeepOnFailure: true,
		LogLimit:      1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed || result.Retained != "fixture" {
		t.Fatalf("result passed=%v retained=%q", result.Passed, result.Retained)
	}
	if runtime.removed {
		t.Fatal("container was removed despite keep-on-failure")
	}
}

func TestVerifyClassifiesExitBeforePortResolutionAsStartupFailure(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.exitBeforePort = true

	result, err := Verify(context.Background(), lifecycleTestConfig(), runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("early container exit unexpectedly passed")
	}
	failed := result.FailedAssertions()
	if len(failed) != 1 || failed[0].Name != "startup.ready" {
		t.Fatalf("failed assertions = %#v, want startup.ready", failed)
	}
	if !runtime.removed {
		t.Fatal("container was not cleaned up")
	}
}

func TestVerifyRequiresTrafficActiveAtSignalConfirmation(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.signalDelay = 120 * time.Millisecond

	result, err := Verify(context.Background(), lifecycleTestConfig(), runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Passed {
		t.Fatal("traffic that completed during signal delivery unexpectedly passed")
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["traffic.inflight_exercised"] {
		t.Fatalf("failed assertions = %#v, want traffic.inflight_exercised", result.FailedAssertions())
	}
	if result.Traffic.Inflight != 0 {
		t.Fatalf("confirmed in-flight requests = %d, want 0", result.Traffic.Inflight)
	}
	if result.Timings.SignalDeliveryMS < 100 {
		t.Fatalf("signal delivery timing = %dms, want at least 100ms", result.Timings.SignalDeliveryMS)
	}
	var requested, confirmed bool
	for _, event := range result.Events {
		if event.Phase != "terminating" {
			continue
		}
		requested = requested || strings.Contains(event.Message, "requested")
		confirmed = confirmed || strings.Contains(event.Message, "delivery confirmed")
	}
	if !requested || !confirmed {
		t.Fatalf("signal events missing request or confirmation: %#v", result.Events)
	}
}

func TestVerifyIncludesSignalCommandLatencyInShutdownDeadline(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.signalDelay = 80 * time.Millisecond
	cfg := lifecycleTestConfig()
	cfg.Shutdown.Deadline = config.NewDuration(50 * time.Millisecond)
	cfg.Assertions.ReadinessWithdrawnWithin = config.NewDuration(40 * time.Millisecond)

	result, err := Verify(context.Background(), cfg, runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["shutdown.deadline"] {
		t.Fatalf("failed assertions = %#v, want shutdown.deadline", result.FailedAssertions())
	}
}

func TestVerifyCleansUpAfterCancellationAtEveryPhase(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*fakeRuntime, context.CancelFunc)
	}{
		{
			name: "start command",
			configure: func(runtime *fakeRuntime, cancel context.CancelFunc) {
				runtime.onStart = cancel
				runtime.startReturnsContextError = true
			},
		},
		{
			name: "readiness polling",
			configure: func(runtime *fakeRuntime, cancel context.CancelFunc) {
				runtime.ready = false
				runtime.onStart = cancel
			},
		},
		{
			name: "signal delivery",
			configure: func(runtime *fakeRuntime, cancel context.CancelFunc) {
				runtime.onSignal = cancel
			},
		},
		{
			name: "shutdown wait",
			configure: func(runtime *fakeRuntime, cancel context.CancelFunc) {
				runtime.ignoreSignal = true
				runtime.onSignal = func() {
					go func() {
						time.Sleep(20 * time.Millisecond)
						cancel()
					}()
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime, server := newFakeRuntime(t, false)
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			test.configure(runtime, cancel)

			result, err := Verify(ctx, lifecycleTestConfig(), runtime, Options{
				PullPolicy: containerruntime.PullNever,
				LogLimit:   1024,
			})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context cancellation", err)
			}
			if result == nil {
				t.Fatal("canceled verification returned no report")
			}
			failed := make(map[string]bool)
			for _, assertion := range result.FailedAssertions() {
				failed[assertion.Name] = true
			}
			if !failed["execution.completed"] {
				t.Fatalf("failed assertions = %#v, want execution.completed", result.FailedAssertions())
			}
			if !runtime.removed || !runtime.removeForce {
				t.Fatalf("cleanup removed=%t force=%t, want forced removal", runtime.removed, runtime.removeForce)
			}
			if result.Retained != "" {
				t.Fatalf("canceled verification retained %q without --keep-on-failure", result.Retained)
			}
		})
	}
}

func TestVerifyKeepOnFailureExplicitlyRetainsAfterCancellation(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	runtime.onStart = cancel
	runtime.startReturnsContextError = true

	result, err := Verify(ctx, lifecycleTestConfig(), runtime, Options{
		PullPolicy:    containerruntime.PullNever,
		KeepOnFailure: true,
		LogLimit:      1024,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if runtime.removed || result.Retained != "fixture" {
		t.Fatalf("removed=%t retained=%q, want explicit retention", runtime.removed, result.Retained)
	}
}

func TestVerifyCleansUpAfterMidRunRuntimeFailure(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.inspectErrorAfter = 2
	runtime.inspectError = errors.New("daemon connection lost")

	result, err := Verify(context.Background(), lifecycleTestConfig(), runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if !errors.Is(err, runtime.inspectError) {
		t.Fatalf("error = %v, want original runtime error", err)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["execution.completed"] || failed["cleanup.completed"] {
		t.Fatalf("failed assertions = %#v", result.FailedAssertions())
	}
	if !runtime.removed || !runtime.removeForce {
		t.Fatalf("cleanup removed=%t force=%t, want forced removal", runtime.removed, runtime.removeForce)
	}
}

func TestVerifyCleansUpAfterRuntimeWaitFailure(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.waitError = errors.New("runtime wait failed")

	result, err := Verify(context.Background(), lifecycleTestConfig(), runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if !errors.Is(err, runtime.waitError) {
		t.Fatalf("error = %v, want original wait error", err)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["execution.completed"] || failed["cleanup.completed"] {
		t.Fatalf("failed assertions = %#v", result.FailedAssertions())
	}
	if !runtime.removed || !runtime.removeForce {
		t.Fatalf("cleanup removed=%t force=%t, want forced removal", runtime.removed, runtime.removeForce)
	}
}

func TestVerifyReturnsCleanupFailureWhenExecutionCompletes(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.removeError = errors.New("cleanup denied")

	result, err := Verify(context.Background(), lifecycleTestConfig(), runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if !errors.Is(err, runtime.removeError) {
		t.Fatalf("error = %v, want cleanup error", err)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["cleanup.completed"] || failed["execution.completed"] {
		t.Fatalf("failed assertions = %#v", result.FailedAssertions())
	}
}

func TestVerifyPreservesExecutionErrorWhenCleanupAlsoFails(t *testing.T) {
	runtime, server := newFakeRuntime(t, false)
	defer server.Close()
	runtime.inspectErrorAfter = 2
	runtime.inspectError = errors.New("daemon connection lost")
	runtime.removeError = errors.New("cleanup denied")

	result, err := Verify(context.Background(), lifecycleTestConfig(), runtime, Options{
		PullPolicy: containerruntime.PullNever,
		LogLimit:   1024,
	})
	if !errors.Is(err, runtime.inspectError) {
		t.Fatalf("error = %v, want original execution error", err)
	}
	failed := make(map[string]bool)
	for _, assertion := range result.FailedAssertions() {
		failed[assertion.Name] = true
	}
	if !failed["execution.completed"] || !failed["cleanup.completed"] {
		t.Fatalf("failed assertions = %#v", result.FailedAssertions())
	}
}

func TestBoundLogsEnforcesEngineLimit(t *testing.T) {
	if got := boundLogs([]byte("untrusted container output"), 9); got != "untrusted" {
		t.Fatalf("boundLogs = %q, want %q", got, "untrusted")
	}
	if got := boundLogs([]byte("untrusted"), 0); got != "" {
		t.Fatalf("boundLogs with zero limit = %q, want empty", got)
	}
}

func TestWaitWithdrawnDoesNotTreatItsOwnDeadlineAsWithdrawal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := waitWithdrawn(ctx, deadlineReadinessChecker{}, time.Millisecond, time.Now())
	if result.withdrawn || !strings.Contains(result.message, "withdrawal deadline expired") {
		t.Fatalf("withdrawal result = %#v, want deadline failure", result)
	}
}

type deadlineReadinessChecker struct{}

func (deadlineReadinessChecker) Check(ctx context.Context) readiness.Observation {
	<-ctx.Done()
	return readiness.Observation{Description: "gRPC DEADLINE_EXCEEDED", Err: errors.New("rpc deadline exceeded")}
}

func (deadlineReadinessChecker) Close() error { return nil }

func lifecycleTestConfig() config.Config {
	cfg := config.Default()
	cfg.Target.Image = "fixture:test"
	cfg.Target.ContainerPort = 8080
	cfg.Readiness.StartupTimeout = config.NewDuration(time.Second)
	cfg.Readiness.Interval = config.NewDuration(10 * time.Millisecond)
	cfg.Traffic.Request.Path = "/work"
	cfg.Traffic.Count = 2
	cfg.Traffic.Concurrency = 2
	cfg.Traffic.ShutdownAfter = config.NewDuration(20 * time.Millisecond)
	cfg.Traffic.RequestTimeout = config.NewDuration(time.Second)
	cfg.Shutdown.Deadline = config.NewDuration(time.Second)
	cfg.Assertions.ReadinessWithdrawnWithin = config.NewDuration(200 * time.Millisecond)
	return cfg
}

type fakeRuntime struct {
	mu                       sync.Mutex
	port                     int
	ready                    bool
	running                  bool
	ignoreSignal             bool
	removed                  bool
	removeForce              bool
	exitBeforePort           bool
	signalDelay              time.Duration
	onStart                  func()
	onSignal                 func()
	startReturnsContextError bool
	inspectCalls             int
	inspectErrorAfter        int
	inspectError             error
	waitCalls                int
	waitError                error
	removeError              error
	exited                   chan struct{}
	exitOnce                 sync.Once
	rejectWorkWhenUnready    bool
	webSocketShutdown        chan struct{}
	webSocketShutdownOnce    sync.Once
	webSocketTerminal        bool
	webSocketCloseCode       websocket.StatusCode
	createdSpec              containerruntime.ContainerSpec
	hostPortCalls            []int
	execCalls                [][]string
	preStopDelay             time.Duration
	preStopExitCode          int
	preStopCompletedAt       time.Time
	signalAt                 time.Time
}

func newFakeRuntime(t *testing.T, ignoreSignal bool) (*fakeRuntime, *httptest.Server) {
	t.Helper()
	runtime := &fakeRuntime{
		ready:              true,
		ignoreSignal:       ignoreSignal,
		exited:             make(chan struct{}),
		webSocketShutdown:  make(chan struct{}),
		webSocketTerminal:  true,
		webSocketCloseCode: websocket.StatusNormalClosure,
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/ready":
			runtime.mu.Lock()
			ready := runtime.ready
			runtime.mu.Unlock()
			if !ready {
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			writer.WriteHeader(http.StatusOK)
		case "/work":
			runtime.mu.Lock()
			reject := runtime.rejectWorkWhenUnready && !runtime.ready
			runtime.mu.Unlock()
			if reject {
				writer.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			time.Sleep(80 * time.Millisecond)
			writer.WriteHeader(http.StatusOK)
		case "/ws":
			connection, err := websocket.Accept(writer, request, nil)
			if err != nil {
				return
			}
			defer func() { _ = connection.CloseNow() }()
			<-runtime.webSocketShutdown
			if runtime.webSocketTerminal {
				if err := connection.Write(request.Context(), websocket.MessageText, []byte("shutdown")); err != nil {
					return
				}
			}
			_ = connection.Close(runtime.webSocketCloseCode, "draining")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	runtime.port, err = strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, server
}

func (f *fakeRuntime) Name() string { return "fake" }

func (f *fakeRuntime) EnsureImage(context.Context, string, containerruntime.PullPolicy) error {
	return nil
}

func (f *fakeRuntime) Create(_ context.Context, spec containerruntime.ContainerSpec) (containerruntime.Container, error) {
	f.mu.Lock()
	f.createdSpec = spec
	f.mu.Unlock()
	return containerruntime.Container{ID: "fixture", Name: "fixture"}, nil
}

func (f *fakeRuntime) Start(ctx context.Context, _ string) error {
	f.mu.Lock()
	f.running = true
	hook := f.onStart
	returnsContextError := f.startReturnsContextError
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if returnsContextError {
		return ctx.Err()
	}
	return nil
}

func (f *fakeRuntime) HostPort(_ context.Context, _ string, containerPort int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostPortCalls = append(f.hostPortCalls, containerPort)
	if f.exitBeforePort {
		f.markExitedLocked()
		return 0, errors.New("port mapping disappeared")
	}
	return f.port, nil
}

func (f *fakeRuntime) Exec(ctx context.Context, _ string, command []string, _ int64) (containerruntime.ExecResult, error) {
	f.mu.Lock()
	f.execCalls = append(f.execCalls, append([]string(nil), command...))
	if len(command) > 0 && command[0] == "/prestop" {
		delay := f.preStopDelay
		exitCode := f.preStopExitCode
		f.mu.Unlock()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			f.mu.Lock()
			f.preStopCompletedAt = time.Now()
			f.mu.Unlock()
			return containerruntime.ExecResult{ExitCode: exitCode}, nil
		case <-ctx.Done():
			return containerruntime.ExecResult{}, ctx.Err()
		}
	}
	exitCode := 1
	if f.ready {
		exitCode = 0
	}
	f.mu.Unlock()
	return containerruntime.ExecResult{ExitCode: exitCode}, nil
}

func (f *fakeRuntime) Signal(context.Context, string, string) error {
	f.mu.Lock()
	f.signalAt = time.Now()
	hook := f.onSignal
	if f.ignoreSignal {
		f.mu.Unlock()
		if hook != nil {
			hook()
		}
		time.Sleep(f.signalDelay)
		return nil
	}
	f.ready = false
	f.webSocketShutdownOnce.Do(func() { close(f.webSocketShutdown) })
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	time.Sleep(f.signalDelay)
	go func() {
		time.Sleep(120 * time.Millisecond)
		f.mu.Lock()
		f.markExitedLocked()
		f.mu.Unlock()
	}()
	return nil
}

func (f *fakeRuntime) Wait(ctx context.Context, _ string) error {
	f.mu.Lock()
	f.waitCalls++
	err := f.waitError
	running := f.running
	exited := f.exited
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	select {
	case <-exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *fakeRuntime) Inspect(context.Context, string) (containerruntime.ContainerState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inspectCalls++
	if f.inspectError != nil && f.inspectCalls >= f.inspectErrorAfter {
		return containerruntime.ContainerState{}, f.inspectError
	}
	state := containerruntime.ContainerState{Running: f.running, Status: "running"}
	if !f.running {
		state.Status = "exited"
	}
	return state, nil
}

func (f *fakeRuntime) Logs(context.Context, string, int64) ([]byte, error) {
	return []byte("fixture logs"), nil
}

func (f *fakeRuntime) Remove(_ context.Context, _ string, force bool) error {
	f.mu.Lock()
	f.markExitedLocked()
	f.removed = true
	f.removeForce = force
	err := f.removeError
	f.mu.Unlock()
	return err
}

func (f *fakeRuntime) markExitedLocked() {
	f.running = false
	f.exitOnce.Do(func() { close(f.exited) })
}
