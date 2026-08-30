package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestWriteJSONAndJUnit(t *testing.T) {
	value := New("abc", "example:test", "fake", time.Now())
	value.AddAssertion("startup.ready", true, "ready")
	value.AddAssertion("shutdown.deadline", false, "too slow")
	value.Finish(time.Now().Add(time.Second))

	directory := t.TempDir()
	jsonPath := filepath.Join(directory, "report.json")
	if err := WriteJSON(jsonPath, value); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(jsonPath) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Passed || len(decoded.Assertions) != 2 {
		t.Fatalf("decoded report = %#v", decoded)
	}

	junitPath := filepath.Join(directory, "report.xml")
	if err := WriteJUnit(junitPath, value); err != nil {
		t.Fatal(err)
	}
	xmlData, err := os.ReadFile(junitPath) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(xmlData), `failures="1"`) || !strings.Contains(string(xmlData), "too slow") {
		t.Fatalf("JUnit report = %s", xmlData)
	}
}

func TestJSONReportSchemaV1RequiredFields(t *testing.T) {
	started := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	value := New("run-1", "example:test", "docker", started)
	value.Container = ContainerSummary{
		ID:        "container-1",
		Name:      "draincheck-run-1",
		Status:    "exited",
		ExitCode:  0,
		OOMKilled: false,
		Error:     "none",
	}
	value.AddEvent(started, "ready", "readiness returned HTTP 200")
	value.AddAssertion("startup.ready", true, "ready")
	value.Finish(started.Add(time.Second))

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, document,
		"schema_version", "run_id", "image", "runtime", "profile", "started_at", "duration_ms", "passed",
		"events", "assertions", "traffic", "streaming", "telemetry", "shutdown", "timings", "container", "forced_cleanup",
	)
	var schemaVersion int
	if err := json.Unmarshal(document["schema_version"], &schemaVersion); err != nil || schemaVersion != 1 {
		t.Fatalf("report schema version = %d, %v; want 1", schemaVersion, err)
	}

	var traffic map[string]json.RawMessage
	if err := json.Unmarshal(document["traffic"], &traffic); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, traffic,
		"driver", "configured", "started", "inflight_at_signal", "completed", "successful", "failed",
		"successful_inflight", "failed_inflight", "post_signal",
	)
	var postSignal map[string]json.RawMessage
	if err := json.Unmarshal(traffic["post_signal"], &postSignal); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, postSignal,
		"policy", "configured", "started", "completed", "accepted", "rejected",
	)
	var streaming map[string]json.RawMessage
	if err := json.Unmarshal(document["streaming"], &streaming); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, streaming, "sse", "websocket")
	var sse map[string]json.RawMessage
	if err := json.Unmarshal(streaming["sse"], &sse); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, sse,
		"enabled", "established", "status", "content_type", "initial_event_received",
		"active_at_signal", "terminal_event_received", "events", "clean_eof",
		"closed_after_signal", "closed_within_timeout", "closed_gracefully", "error_kind", "error",
	)
	var webSocket map[string]json.RawMessage
	if err := json.Unmarshal(streaming["websocket"], &webSocket); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, webSocket,
		"enabled", "established", "status", "negotiated_subprotocol", "active_at_signal",
		"messages", "terminal_message_received", "terminal_message_after_signal",
		"close_frame_received", "close_code", "close_reason", "closed_after_signal",
		"closed_within_timeout", "closed_gracefully", "error_kind", "error",
	)
	var telemetry map[string]json.RawMessage
	if err := json.Unmarshal(document["telemetry"], &telemetry); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, telemetry,
		"enabled", "protocol", "eligible_inflight_requests", "minimum_correlated_spans",
		"correlated_spans", "matched_requests", "export_requests", "rejected_export_requests", "metrics",
	)
	var metrics map[string]json.RawMessage
	if err := json.Unmarshal(telemetry["metrics"], &metrics); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, metrics, "enabled", "minimum_data_points", "data_points", "export_requests")
	var timings map[string]json.RawMessage
	if err := json.Unmarshal(document["timings"], &timings); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, timings,
		"startup_ready_ms", "pre_stop_ms", "signal_delivery_ms", "readiness_withdrawal_ms", "container_exit_ms", "shutdown_total_ms",
	)
	var shutdown map[string]json.RawMessage
	if err := json.Unmarshal(document["shutdown"], &shutdown); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, shutdown, "deadline_ms", "pre_stop")
	var preStop map[string]json.RawMessage
	if err := json.Unmarshal(shutdown["pre_stop"], &preStop); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, preStop, "configured", "exit_code", "duration_ms", "timed_out")
	var container map[string]json.RawMessage
	if err := json.Unmarshal(document["container"], &container); err != nil {
		t.Fatal(err)
	}
	requireJSONFields(t, container, "id", "name", "status", "exit_code", "oom_killed", "error")

	var events []map[string]json.RawMessage
	if err := json.Unmarshal(document["events"], &events); err != nil || len(events) != 1 {
		t.Fatalf("events = %#v, %v", events, err)
	}
	requireJSONFields(t, events[0], "elapsed_ms", "phase", "message")
	var assertions []map[string]json.RawMessage
	if err := json.Unmarshal(document["assertions"], &assertions); err != nil || len(assertions) != 1 {
		t.Fatalf("assertions = %#v, %v", assertions, err)
	}
	requireJSONFields(t, assertions[0], "name", "passed", "message")
}

func requireJSONFields(t *testing.T, document map[string]json.RawMessage, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if _, found := document[field]; !found {
			t.Errorf("stable JSON schema is missing %q; fields are %v", field, sortedJSONFields(document))
		}
	}
}

func sortedJSONFields(document map[string]json.RawMessage) []string {
	fields := make([]string, 0, len(document))
	for field := range document {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func TestFinishSortsConcurrentEvidenceByElapsedTime(t *testing.T) {
	started := time.Now()
	value := New("abc", "example:test", "fake", started)
	value.AddEvent(started.Add(2*time.Second), "exit", "exited")
	value.AddEvent(started.Add(time.Second), "readiness", "withdrawn")
	value.AddAssertion("startup.ready", true, "ready")
	value.Finish(started.Add(3 * time.Second))
	if value.Events[0].Phase != "readiness" || value.Events[1].Phase != "exit" {
		t.Fatalf("events not sorted: %#v", value.Events)
	}
}

func TestDiagnosticHintsForTrafficTiming(t *testing.T) {
	for _, test := range []struct {
		assertion string
		contains  string
	}{
		{assertion: "traffic.inflight_exercised", contains: "remains active"},
		{assertion: "traffic.failed_requests", contains: "shutdown ordering"},
		{assertion: "traffic.post_signal_policy", contains: "post_signal.policy"},
		{assertion: "stream.established", contains: "SSE path"},
		{assertion: "stream.active_at_signal", contains: "connection open"},
		{assertion: "stream.closed_gracefully", contains: "terminal SSE event"},
		{assertion: "websocket.established", contains: "opening handshake"},
		{assertion: "websocket.active_at_signal", contains: "connection open"},
		{assertion: "websocket.closed_gracefully", contains: "terminal WebSocket message"},
		{assertion: "grpc_stream.established", contains: "gRPC method"},
		{assertion: "grpc_stream.active_at_signal", contains: "server-streaming RPC open"},
		{assertion: "grpc_stream.closed_gracefully", contains: "configured gRPC status"},
		{assertion: "telemetry.spans_exported", contains: "tracer provider"},
		{assertion: "telemetry.metrics_exported", contains: "meter provider"},
	} {
		hint := diagnosticHint([]Assertion{{Name: test.assertion}})
		if !strings.Contains(hint, test.contains) {
			t.Errorf("diagnosticHint(%q) = %q, want text containing %q", test.assertion, hint, test.contains)
		}
	}
}

func TestDiagnosticHintsForCommandProbeFailures(t *testing.T) {
	for _, test := range []struct {
		assertion Assertion
		contains  string
	}{
		{assertion: Assertion{Name: "traffic.inflight_exercised", Message: "command probes finished before reporting active work"}, contains: "active event"},
		{assertion: Assertion{Name: "traffic.inflight_exercised", Message: "prepare gRPC traffic: reflection unavailable"}, contains: "descriptor-set"},
		{assertion: Assertion{Name: "traffic.failed_requests", Message: "1 failed requests; protocol_json=1"}, contains: "command probe protocol"},
	} {
		hint := diagnosticHint([]Assertion{test.assertion})
		if !strings.Contains(hint, test.contains) {
			t.Errorf("diagnosticHint(%#v) = %q, want %q", test.assertion, hint, test.contains)
		}
	}
}

func TestDiagnosticHintForGRPCTrafficFailure(t *testing.T) {
	hint := diagnosticHint([]Assertion{{Name: "traffic.failed_requests", Message: "1 failed requests; gRPC UNAVAILABLE=1"}})
	if !strings.Contains(hint, "gRPC status") {
		t.Fatalf("hint = %q", hint)
	}
}
