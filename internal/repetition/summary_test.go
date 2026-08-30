package repetition

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssubedir/draincheck/internal/report"
)

func TestSummaryAggregatesOnlyPassingRunTimings(t *testing.T) {
	started := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	summary := New("example:test", "fake", 4, Budgets{}, started)
	for index, duration := range []int64{300, 100, 200} {
		value := passingReport(index+1, duration)
		summary.Add(index+1, value, nil, filepath.Join("runs", value.RunID))
	}
	failed := passingReport(4, 900)
	failed.AddAssertion("shutdown.deadline", false, "too slow")
	failed.Finish(time.Now())
	summary.Add(4, failed, nil, filepath.Join("runs", failed.RunID))
	summary.Finish(started.Add(2 * time.Second))

	if summary.Passed || summary.RunsPassed != 3 || summary.RunsFailed != 1 {
		t.Fatalf("unexpected verdict: %#v", summary)
	}
	stats := summary.Timings.Verification
	if stats.Samples != 3 || stats.MinMS != 100 || stats.P50MS != 200 || stats.P95MS != 300 || stats.MaxMS != 300 {
		t.Fatalf("verification stats = %#v", stats)
	}
}

func TestSummaryAggregatesPreStopOnlyWhenConfigured(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 2, Budgets{}, started)
	withoutHook := passingReport(1, 100)
	summary.Add(1, withoutHook, nil, "runs/one")
	withHook := passingReport(2, 200)
	withHook.Shutdown.PreStop.Configured = true
	withHook.Timings.PreStopMS = 17
	summary.Add(2, withHook, nil, "runs/two")
	summary.Finish(started.Add(time.Second))

	if summary.Timings.PreStop.Samples != 1 || summary.Timings.PreStop.P95MS != 17 {
		t.Fatalf("pre-stop timing stats = %#v", summary.Timings.PreStop)
	}
}

func TestSummaryRecordsExecutionErrorAndIncompleteRuns(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 3, Budgets{}, started)
	summary.Add(1, nil, errors.New("runtime unavailable"), "")
	summary.Finish(started.Add(time.Second))
	if summary.Passed || summary.ExecutionErrors != 1 || summary.RunsCompleted != 1 {
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
	if decoded.SchemaVersion != 1 || decoded.RunsRequested != 3 {
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
	var suite repeatJUnitSuite
	if err := xml.Unmarshal(xmlData, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 3 || suite.Errors != 1 || suite.Skipped != 2 || len(suite.Cases) != 3 {
		t.Fatalf("JUnit suite = %#v", suite)
	}
}

func TestSummaryJSONSchemaV1RequiredFields(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 2, Budgets{}, started)
	value := passingReport(1, 100)
	summary.Add(1, value, nil, "runs/one")
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
		"runs_requested",
		"runs_completed",
		"runs_passed",
		"runs_failed",
		"execution_errors",
		"budget_failures",
		"passed",
		"runs",
		"timings",
		"budget_assertions",
	} {
		if _, found := document[field]; !found {
			t.Errorf("repeat summary schema is missing %q", field)
		}
	}
}

func TestWriteHumanShowsFailedAssertions(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 2, Budgets{}, started)
	value := passingReport(1, 100)
	value.AddAssertion("readiness.withdrawn", false, "still ready")
	value.Finish(time.Now())
	summary.Add(1, value, nil, "runs/one")
	summary.Finish(started.Add(time.Second))
	var output strings.Builder
	summary.WriteHuman(&output)
	if !strings.Contains(output.String(), "REPEAT FAIL") || !strings.Contains(output.String(), "readiness.withdrawn") {
		t.Fatalf("human summary = %q", output.String())
	}
}

func TestSummaryFailsExceededBudgetWithoutFailingRuns(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 3, Budgets{
		StartupReadyP95:        60 * time.Millisecond,
		ReadinessWithdrawalP95: 25 * time.Millisecond,
	}, started)
	for index, duration := range []int64{100, 200, 300} {
		summary.Add(index+1, passingReport(index+1, duration), nil, fmt.Sprintf("runs/%d", index+1))
	}
	summary.Finish(started.Add(time.Second))

	if summary.Passed || summary.RunsPassed != 3 || summary.RunsFailed != 0 || summary.BudgetFailures != 1 {
		t.Fatalf("unexpected budget verdict: %#v", summary)
	}
	if len(summary.BudgetAssertions) != 2 {
		t.Fatalf("budget assertions = %#v", summary.BudgetAssertions)
	}
	startup := summary.BudgetAssertions[0]
	if startup.Name != BudgetStartupReadyP95 || !startup.Evaluated || startup.Passed || startup.ObservedP95MS != 75 || startup.LimitMS != 60 || startup.Samples != 3 {
		t.Fatalf("startup budget assertion = %#v", startup)
	}
	withdrawal := summary.BudgetAssertions[1]
	if withdrawal.Name != BudgetReadinessWithdrawalP95 || !withdrawal.Evaluated || !withdrawal.Passed {
		t.Fatalf("withdrawal budget assertion = %#v", withdrawal)
	}

	directory := t.TempDir()
	junitPath := filepath.Join(directory, "summary.xml")
	if err := WriteJUnit(junitPath, summary); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(junitPath) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var suite repeatJUnitSuite
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 5 || suite.Failures != 1 || suite.Errors != 0 || suite.Skipped != 0 || len(suite.Cases) != 5 {
		t.Fatalf("budget JUnit suite = %#v", suite)
	}
	if suite.Cases[3].Name != BudgetStartupReadyP95 || suite.Cases[3].Classname != "draincheck.repeat.budget" || suite.Cases[3].Failure == nil {
		t.Fatalf("failed budget JUnit case = %#v", suite.Cases[3])
	}
	if suite.Cases[4].Name != BudgetReadinessWithdrawalP95 || suite.Cases[4].Failure != nil {
		t.Fatalf("passing budget JUnit case = %#v", suite.Cases[4])
	}
}

func TestSummarySkipsBudgetsWhenLifecycleSampleIsIncomplete(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 2, Budgets{ContainerExitP95: time.Second}, started)
	summary.Add(1, passingReport(1, 100), nil, "runs/one")
	failed := passingReport(2, 200)
	failed.AddAssertion("shutdown.deadline", false, "too slow")
	failed.Finish(time.Now())
	summary.Add(2, failed, nil, "runs/two")
	summary.Finish(started.Add(time.Second))

	if len(summary.BudgetAssertions) != 1 || summary.BudgetAssertions[0].Evaluated || summary.BudgetFailures != 0 {
		t.Fatalf("budget assertions = %#v", summary.BudgetAssertions)
	}
	if !strings.Contains(summary.BudgetAssertions[0].Message, "1/2 runs") {
		t.Fatalf("budget message = %q", summary.BudgetAssertions[0].Message)
	}
	directory := t.TempDir()
	junitPath := filepath.Join(directory, "summary.xml")
	if err := WriteJUnit(junitPath, summary); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(junitPath) // #nosec G304 -- Test paths are created under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var suite repeatJUnitSuite
	if err := xml.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Tests != 3 || suite.Failures != 1 || suite.Skipped != 1 {
		t.Fatalf("skipped-budget JUnit suite = %#v", suite)
	}
}

func TestBudgetAssertionJSONRequiredFields(t *testing.T) {
	started := time.Now()
	summary := New("example:test", "fake", 2, Budgets{StartupReadyP95: time.Second}, started)
	summary.Add(1, passingReport(1, 100), nil, "runs/one")
	summary.Add(2, passingReport(2, 200), nil, "runs/two")
	summary.Finish(started.Add(time.Second))

	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var assertions []map[string]json.RawMessage
	if err := json.Unmarshal(document["budget_assertions"], &assertions); err != nil || len(assertions) != 1 {
		t.Fatalf("budget assertions = %#v, %v", assertions, err)
	}
	for _, field := range []string{"name", "evaluated", "passed", "samples", "observed_p95_ms", "limit_ms", "message"} {
		if _, found := assertions[0][field]; !found {
			t.Errorf("budget assertion schema is missing %q", field)
		}
	}
}

func passingReport(index int, durationMS int64) *report.Report {
	started := time.Unix(int64(index), 0)
	value := report.New(fmt.Sprintf("run-%d", index), "example:test", "fake", started)
	value.Timings = report.TimingSummary{
		StartupReadyMS:        durationMS / 4,
		PreStopMS:             5,
		SignalDeliveryMS:      10,
		ReadinessWithdrawalMS: 20,
		ContainerExitMS:       durationMS / 2,
		ShutdownTotalMS:       durationMS/2 + 5,
	}
	value.AddAssertion("startup.ready", true, "ready")
	value.Finish(started.Add(time.Duration(durationMS) * time.Millisecond))
	return value
}
