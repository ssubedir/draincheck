package suite

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ssubedir/draincheck/internal/report"
)

const SchemaVersion = 1

// Summary is the machine-readable result of sequential, isolated lifecycle scenarios.
type Summary struct {
	SchemaVersion      int               `json:"schema_version"`
	Image              string            `json:"image"`
	Runtime            string            `json:"runtime"`
	Profile            string            `json:"profile"`
	StartedAt          time.Time         `json:"started_at"`
	DurationMS         int64             `json:"duration_ms"`
	ScenariosRequested int               `json:"scenarios_requested"`
	ScenariosCompleted int               `json:"scenarios_completed"`
	ScenariosPassed    int               `json:"scenarios_passed"`
	ScenariosFailed    int               `json:"scenarios_failed"`
	ExecutionErrors    int               `json:"execution_errors"`
	Passed             bool              `json:"passed"`
	Scenarios          []ScenarioSummary `json:"scenarios"`
	startedMono        time.Time
	planned            []Definition
}

type Definition struct {
	Name   string
	Config string
}

type ScenarioSummary struct {
	Name              string               `json:"name"`
	Config            string               `json:"config"`
	RunID             string               `json:"run_id,omitempty"`
	Passed            bool                 `json:"passed"`
	DurationMS        int64                `json:"duration_ms"`
	Timings           report.TimingSummary `json:"timings"`
	FailedAssertions  []string             `json:"failed_assertions,omitempty"`
	Error             string               `json:"error,omitempty"`
	ArtifactDirectory string               `json:"artifact_directory,omitempty"`
}

func New(image, runtimeName string, scenarios []Definition, now time.Time) *Summary {
	return &Summary{
		SchemaVersion:      SchemaVersion,
		Image:              image,
		Runtime:            runtimeName,
		Profile:            "generic",
		StartedAt:          now.UTC(),
		ScenariosRequested: len(scenarios),
		Scenarios:          make([]ScenarioSummary, 0, len(scenarios)),
		startedMono:        now,
		planned:            append([]Definition(nil), scenarios...),
	}
}

func (s *Summary) Add(name, configPath string, result *report.Report, runErr error, artifactDirectory string) {
	scenario := ScenarioSummary{
		Name:              name,
		Config:            configPath,
		ArtifactDirectory: artifactDirectory,
	}
	if result != nil {
		scenario.RunID = result.RunID
		scenario.DurationMS = result.DurationMS
		scenario.Timings = result.Timings
		for _, assertion := range result.FailedAssertions() {
			scenario.FailedAssertions = append(scenario.FailedAssertions, assertion.Name)
		}
	}
	if runErr != nil {
		scenario.Error = runErr.Error()
		s.ExecutionErrors++
	}
	scenario.Passed = result != nil && result.Passed && runErr == nil
	s.Scenarios = append(s.Scenarios, scenario)
	s.ScenariosCompleted++
	if scenario.Passed {
		s.ScenariosPassed++
	} else {
		s.ScenariosFailed++
	}
}

func (s *Summary) Finish(now time.Time) {
	s.DurationMS = now.Sub(s.startedMono).Milliseconds()
	s.Passed = s.ScenariosCompleted == s.ScenariosRequested && s.ScenariosFailed == 0
}

func (s *Summary) WriteHuman(writer io.Writer) {
	verdict := "PASS"
	if !s.Passed {
		verdict = "FAIL"
	}
	_, _ = fmt.Fprintf(
		writer,
		"\nSUITE %s %s: %d/%d scenarios passed (%s)\n",
		verdict,
		s.Image,
		s.ScenariosPassed,
		s.ScenariosRequested,
		time.Duration(s.DurationMS)*time.Millisecond,
	)
	if s.ScenariosFailed == 0 {
		return
	}
	_, _ = fmt.Fprintln(writer, "\nFailed scenarios:")
	for _, scenario := range s.Scenarios {
		if scenario.Passed {
			continue
		}
		detail := scenario.Error
		if detail == "" {
			detail = strings.Join(scenario.FailedAssertions, ", ")
		}
		_, _ = fmt.Fprintf(writer, "  - %s: %s\n", scenario.Name, detail)
	}
}
