package suite

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssubedir/draincheck/internal/report"
)

func TestSummaryAggregatesNamedScenarios(t *testing.T) {
	started := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	summary := New("example:test", "fake", []Definition{
		{Name: "http", Config: "scenarios/http.yaml"},
		{Name: "grpc", Config: "scenarios/grpc.yaml"},
	}, started)
	passing := suiteReport("run-http", true)
	summary.Add("http", "scenarios/http.yaml", passing, nil, "scenarios/http")
	failing := suiteReport("run-grpc", false)
	summary.Add("grpc", "scenarios/grpc.yaml", failing, nil, "scenarios/grpc")
	summary.Finish(started.Add(2 * time.Second))

	if summary.Passed || summary.ScenariosCompleted != 2 || summary.ScenariosPassed != 1 || summary.ScenariosFailed != 1 || summary.ExecutionErrors != 0 {
		t.Fatalf("unexpected suite verdict: %#v", summary)
	}
	if got := summary.Scenarios[1]; got.Name != "grpc" || got.RunID != "run-grpc" || len(got.FailedAssertions) != 1 || got.FailedAssertions[0] != "shutdown.deadline" {
		t.Fatalf("failed scenario = %#v", got)
	}
	var output strings.Builder
	summary.WriteHuman(&output)
	if !strings.Contains(output.String(), "SUITE FAIL") || !strings.Contains(output.String(), "grpc: shutdown.deadline") {
		t.Fatalf("human summary = %q", output.String())
	}
}

func TestSummaryReportsExecutionErrorAndSkippedScenario(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", []Definition{
		{Name: "first", Config: "first.yaml"},
		{Name: "second", Config: "second.yaml"},
		{Name: "third", Config: "third.yaml"},
	}, started)
	summary.Add("first", "first.yaml", nil, errors.New("runtime unavailable"), "")
	summary.Finish(started.Add(time.Second))

	if summary.Passed || summary.ExecutionErrors != 1 || summary.ScenariosCompleted != 1 {
		t.Fatalf("unexpected execution summary: %#v", summary)
	}
	directory := t.TempDir()
	jsonPath := filepath.Join(directory, "summary.json")
	if err := WriteJSON(jsonPath, summary); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(jsonPath) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var decoded Summary
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 1 || decoded.ScenariosRequested != 3 || len(decoded.Scenarios) != 1 {
		t.Fatalf("decoded summary = %#v", decoded)
	}

	junitPath := filepath.Join(directory, "summary.xml")
	if err := WriteJUnit(junitPath, summary); err != nil {
		t.Fatal(err)
	}
	xmlData, err := os.ReadFile(junitPath) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var junit suiteJUnitSuite
	if err := xml.Unmarshal(xmlData, &junit); err != nil {
		t.Fatal(err)
	}
	if junit.Tests != 3 || junit.Errors != 1 || junit.Skipped != 2 || len(junit.Cases) != 3 {
		t.Fatalf("JUnit suite = %#v", junit)
	}
	if junit.Cases[1].Name != "second" || junit.Cases[1].Skipped == nil || junit.Cases[2].Name != "third" || junit.Cases[2].Skipped == nil {
		t.Fatalf("skipped JUnit cases = %#v", junit.Cases)
	}
}

func TestSummaryJSONSchemaV1RequiredFields(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", []Definition{{Name: "http", Config: "http.yaml"}}, started)
	summary.Add("http", "http.yaml", suiteReport("run", true), nil, "scenarios/http")
	summary.Finish(started.Add(time.Second))
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"schema_version",
		"image",
		"runtime",
		"profile",
		"started_at",
		"duration_ms",
		"scenarios_requested",
		"scenarios_completed",
		"scenarios_passed",
		"scenarios_failed",
		"execution_errors",
		"passed",
		"scenarios",
	} {
		if _, found := document[field]; !found {
			t.Errorf("suite summary schema is missing %q", field)
		}
	}
}

func suiteReport(runID string, passed bool) *report.Report {
	started := time.Now()
	value := report.New(runID, "example:test", "fake", started)
	value.Timings = report.TimingSummary{
		StartupReadyMS:        10,
		PreStopMS:             1,
		SignalDeliveryMS:      2,
		ReadinessWithdrawalMS: 4,
		ContainerExitMS:       20,
		ShutdownTotalMS:       21,
	}
	value.AddAssertion("startup.ready", true, "ready")
	if !passed {
		value.AddAssertion("shutdown.deadline", false, "too slow")
	}
	value.Finish(started.Add(50 * time.Millisecond))
	return value
}
