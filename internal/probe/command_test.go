package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandRunReportsActiveAndSuccessfulResult(t *testing.T) {
	run := Start(context.Background(), helperSpec(t, "success"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run.WaitStarted(ctx); err != nil {
		t.Fatal(err)
	}
	if active := run.StopAndSnapshot(); len(active) != 1 || active[0] != 1 {
		t.Fatalf("active snapshot = %v", active)
	}
	waitProbe(t, run)
	results := run.Results()
	if len(results) != 1 || !results[0].Success || results[0].ErrorKind != "" {
		t.Fatalf("results = %#v", results)
	}
}

func TestCommandRunInjectsReservedEnvironment(t *testing.T) {
	spec := helperSpec(t, "environment")
	spec.BaseURL = "http://127.0.0.1:4321"
	spec.RunID = "run-example"
	spec.Phase = PhasePostSignal
	spec.Environment["PROBE_CUSTOM"] = "configured"
	run := Start(context.Background(), spec)
	waitProbe(t, run)
	if results := run.Results(); len(results) != 1 || !results[0].Success {
		t.Fatalf("results = %#v", results)
	}
}

func TestCommandRunClassifiesProtocolAndExitFailures(t *testing.T) {
	for _, test := range []struct {
		mode string
		kind string
	}{
		{mode: "missing-active", kind: "protocol_order"},
		{mode: "missing-result", kind: "protocol_missing_result"},
		{mode: "malformed", kind: "protocol_json"},
		{mode: "unknown-field", kind: "protocol_json"},
		{mode: "active-message", kind: "protocol_order"},
		{mode: "message-limit", kind: "protocol_message_limit"},
		{mode: "output-limit", kind: "protocol_output_limit"},
		{mode: "line-limit", kind: "protocol_line_limit"},
		{mode: "nonzero", kind: "exit_code"},
		{mode: "result-failure", kind: "probe_result"},
	} {
		t.Run(test.mode, func(t *testing.T) {
			run := Start(context.Background(), helperSpec(t, test.mode))
			waitProbe(t, run)
			results := run.Results()
			if len(results) != 1 || results[0].Success || results[0].ErrorKind != test.kind {
				t.Fatalf("results = %#v, want failure kind %q", results, test.kind)
			}
		})
	}
}

func TestCommandRunTimesOutAndStopsProcess(t *testing.T) {
	spec := helperSpec(t, "timeout")
	spec.Timeout = 100 * time.Millisecond
	run := Start(context.Background(), spec)
	waitProbe(t, run)
	results := run.Results()
	if len(results) != 1 || results[0].ErrorKind != "timeout" {
		t.Fatalf("results = %#v", results)
	}
}

func TestCommandRunRequiresAnActiveBarrier(t *testing.T) {
	run := Start(context.Background(), helperSpec(t, "malformed"))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run.WaitStarted(ctx); err == nil || !strings.Contains(err.Error(), "before reporting active work") {
		t.Fatalf("WaitStarted error = %v", err)
	}
}

func TestCommandRunStopsSchedulingAfterSnapshot(t *testing.T) {
	spec := helperSpec(t, "success")
	spec.Count = 3
	spec.Concurrency = 1
	run := Start(context.Background(), spec)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := run.WaitStarted(ctx); err != nil {
		t.Fatal(err)
	}
	run.StopAndSnapshot()
	waitProbe(t, run)
	if run.StartedCount() != 1 || len(run.Results()) != 1 {
		t.Fatalf("started/results = %d/%d, want 1/1", run.StartedCount(), len(run.Results()))
	}
}

func TestCommandProbeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_DRAINCHECK_PROBE_HELPER") != "1" {
		return
	}
	mode := os.Getenv("DRAINCHECK_TEST_PROBE_MODE")
	active := func() { _, _ = fmt.Fprintln(os.Stdout, `{"type":"active"}`) }
	result := func(success bool) {
		_, _ = fmt.Fprintf(os.Stdout, "{\"type\":\"result\",\"success\":%t}\n", success)
	}
	switch mode {
	case "success":
		active()
		time.Sleep(150 * time.Millisecond)
		result(true)
	case "environment":
		if os.Getenv("DRAINCHECK_PROTOCOL_VERSION") != ProtocolVersion ||
			os.Getenv("DRAINCHECK_TARGET_URL") != "http://127.0.0.1:4321" ||
			os.Getenv("DRAINCHECK_RUN_ID") != "run-example" ||
			os.Getenv("DRAINCHECK_REQUEST_ID") != "1" ||
			os.Getenv("DRAINCHECK_PHASE") != PhasePostSignal ||
			os.Getenv("PROBE_CUSTOM") != "configured" {
			os.Exit(9)
		}
		active()
		result(true)
	case "missing-active":
		result(true)
	case "missing-result":
		active()
	case "malformed":
		_, _ = fmt.Fprintln(os.Stdout, "not-json")
	case "unknown-field":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"active","unknown":true}`)
	case "active-message":
		_, _ = fmt.Fprintln(os.Stdout, `{"type":"active","message":"too early"}`)
	case "message-limit":
		active()
		_, _ = fmt.Fprintf(os.Stdout, `{"type":"result","success":true,"message":"%s"}`+"\n", strings.Repeat("x", maxMessageBytes+1))
	case "output-limit":
		active()
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("\n", maxProtocolBytes))
	case "line-limit":
		_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", maxProtocolLine+1))
	case "nonzero":
		active()
		result(true)
		fmt.Fprintln(os.Stderr, "intentional nonzero exit")
		os.Exit(7)
	case "result-failure":
		active()
		result(false)
	case "timeout":
		active()
		time.Sleep(5 * time.Second)
	default:
		os.Exit(8)
	}
	os.Exit(0)
}

func helperSpec(t *testing.T, mode string) Spec {
	t.Helper()
	executable, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return Spec{
		Executable: executable,
		Args:       []string{"-test.run=^TestCommandProbeHelperProcess$"},
		Directory:  t.TempDir(),
		Environment: map[string]string{
			"GO_WANT_DRAINCHECK_PROBE_HELPER": "1",
			"DRAINCHECK_TEST_PROBE_MODE":      mode,
		},
		BaseURL:     "http://127.0.0.1:8080",
		RunID:       "run-test",
		Phase:       PhaseInitial,
		Count:       1,
		Concurrency: 1,
		Timeout:     2 * time.Second,
	}
}

func waitProbe(t *testing.T, run *Run) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("command probe did not finish")
	}
}
