package repetition

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ssubedir/draincheck/internal/report"
)

const SchemaVersion = 1

const (
	BudgetStartupReadyP95        = "repeat.startup_ready_p95"
	BudgetReadinessWithdrawalP95 = "repeat.readiness_withdrawal_p95"
	BudgetContainerExitP95       = "repeat.container_exit_p95"
)

type Budgets struct {
	StartupReadyP95        time.Duration
	ReadinessWithdrawalP95 time.Duration
	ContainerExitP95       time.Duration
}

// Summary is the machine-readable result of sequential, isolated lifecycle verifications.
type Summary struct {
	SchemaVersion    int               `json:"schema_version"`
	Image            string            `json:"image"`
	Runtime          string            `json:"runtime"`
	Profile          string            `json:"profile"`
	StartedAt        time.Time         `json:"started_at"`
	DurationMS       int64             `json:"duration_ms"`
	RunsRequested    int               `json:"runs_requested"`
	RunsCompleted    int               `json:"runs_completed"`
	RunsPassed       int               `json:"runs_passed"`
	RunsFailed       int               `json:"runs_failed"`
	ExecutionErrors  int               `json:"execution_errors"`
	BudgetFailures   int               `json:"budget_failures"`
	Passed           bool              `json:"passed"`
	Runs             []RunSummary      `json:"runs"`
	Timings          AggregateTimings  `json:"timings"`
	BudgetAssertions []BudgetAssertion `json:"budget_assertions"`
	startedMono      time.Time
	verificationMS   []int64
	timingSamples    []report.TimingSummary
	preStopMS        []int64
	budgets          Budgets
}

type RunSummary struct {
	Index             int      `json:"index"`
	RunID             string   `json:"run_id,omitempty"`
	Passed            bool     `json:"passed"`
	DurationMS        int64    `json:"duration_ms"`
	FailedAssertions  []string `json:"failed_assertions,omitempty"`
	Error             string   `json:"error,omitempty"`
	ArtifactDirectory string   `json:"artifact_directory,omitempty"`
}

type AggregateTimings struct {
	Verification        DurationStats `json:"verification"`
	StartupReady        DurationStats `json:"startup_ready"`
	PreStop             DurationStats `json:"pre_stop"`
	SignalDelivery      DurationStats `json:"signal_delivery"`
	ReadinessWithdrawal DurationStats `json:"readiness_withdrawal"`
	ContainerExit       DurationStats `json:"container_exit"`
	ShutdownTotal       DurationStats `json:"shutdown_total"`
}

type DurationStats struct {
	Samples int   `json:"samples"`
	MinMS   int64 `json:"min_ms"`
	P50MS   int64 `json:"p50_ms"`
	P95MS   int64 `json:"p95_ms"`
	MaxMS   int64 `json:"max_ms"`
}

type BudgetAssertion struct {
	Name          string `json:"name"`
	Evaluated     bool   `json:"evaluated"`
	Passed        bool   `json:"passed"`
	Samples       int    `json:"samples"`
	ObservedP95MS int64  `json:"observed_p95_ms"`
	LimitMS       int64  `json:"limit_ms"`
	Message       string `json:"message"`
}

func New(image, runtimeName string, runs int, budgets Budgets, now time.Time) *Summary {
	return &Summary{
		SchemaVersion:    SchemaVersion,
		Image:            image,
		Runtime:          runtimeName,
		Profile:          "generic",
		StartedAt:        now.UTC(),
		RunsRequested:    runs,
		Runs:             make([]RunSummary, 0, runs),
		BudgetAssertions: make([]BudgetAssertion, 0, 3),
		startedMono:      now,
		budgets:          budgets,
	}
}

func (s *Summary) Add(index int, result *report.Report, runErr error, artifactDirectory string) {
	run := RunSummary{Index: index, ArtifactDirectory: artifactDirectory}
	if result != nil {
		run.RunID = result.RunID
		run.DurationMS = result.DurationMS
		for _, assertion := range result.FailedAssertions() {
			run.FailedAssertions = append(run.FailedAssertions, assertion.Name)
		}
	}
	if runErr != nil {
		run.Error = runErr.Error()
		s.ExecutionErrors++
	}
	run.Passed = result != nil && result.Passed && runErr == nil
	s.Runs = append(s.Runs, run)
	s.RunsCompleted++
	if run.Passed {
		s.RunsPassed++
		s.verificationMS = append(s.verificationMS, result.DurationMS)
		s.timingSamples = append(s.timingSamples, result.Timings)
		if result.Shutdown.PreStop.Configured {
			s.preStopMS = append(s.preStopMS, result.Timings.PreStopMS)
		}
	} else {
		s.RunsFailed++
	}
}

func (s *Summary) Finish(now time.Time) {
	s.DurationMS = now.Sub(s.startedMono).Milliseconds()
	lifecyclePassed := s.RunsCompleted == s.RunsRequested && s.RunsFailed == 0

	var startup []int64
	var signal []int64
	var withdrawal []int64
	var exit []int64
	var shutdownTotal []int64
	for _, value := range s.timingSamples {
		startup = append(startup, value.StartupReadyMS)
		signal = append(signal, value.SignalDeliveryMS)
		withdrawal = append(withdrawal, value.ReadinessWithdrawalMS)
		exit = append(exit, value.ContainerExitMS)
		shutdownTotal = append(shutdownTotal, value.ShutdownTotalMS)
	}
	s.Timings = AggregateTimings{
		Verification:        calculateStats(s.verificationMS),
		StartupReady:        calculateStats(startup),
		PreStop:             calculateStats(s.preStopMS),
		SignalDelivery:      calculateStats(signal),
		ReadinessWithdrawal: calculateStats(withdrawal),
		ContainerExit:       calculateStats(exit),
		ShutdownTotal:       calculateStats(shutdownTotal),
	}
	s.evaluateBudgets(lifecyclePassed)
	s.Passed = lifecyclePassed && s.BudgetFailures == 0
}

func (s *Summary) WriteHuman(writer io.Writer) {
	verdict := "PASS"
	if !s.Passed {
		verdict = "FAIL"
	}
	_, _ = fmt.Fprintf(
		writer,
		"\nREPEAT %s %s: %d/%d runs passed (%s)\n",
		verdict,
		s.Image,
		s.RunsPassed,
		s.RunsRequested,
		time.Duration(s.DurationMS)*time.Millisecond,
	)
	if s.Timings.Verification.Samples > 0 {
		_, _ = fmt.Fprintln(writer, "\nPassing-run timing distribution:")
		writeTiming(writer, "verification", s.Timings.Verification)
		writeTiming(writer, "startup ready", s.Timings.StartupReady)
		if s.Timings.PreStop.Samples > 0 {
			writeTiming(writer, "pre-stop", s.Timings.PreStop)
		}
		writeTiming(writer, "signal delivery", s.Timings.SignalDelivery)
		writeTiming(writer, "readiness withdrawal", s.Timings.ReadinessWithdrawal)
		writeTiming(writer, "container exit", s.Timings.ContainerExit)
		writeTiming(writer, "shutdown total", s.Timings.ShutdownTotal)
	}
	if len(s.BudgetAssertions) > 0 {
		_, _ = fmt.Fprintln(writer, "\nRepeat budget assertions:")
		for _, assertion := range s.BudgetAssertions {
			verdict := "SKIP"
			if assertion.Evaluated {
				verdict = "PASS"
				if !assertion.Passed {
					verdict = "FAIL"
				}
			}
			_, _ = fmt.Fprintf(writer, "  - %-4s %s: %s\n", verdict, assertion.Name, assertion.Message)
		}
	}
	if s.RunsFailed > 0 {
		_, _ = fmt.Fprintln(writer, "\nFailed runs:")
		for _, run := range s.Runs {
			if run.Passed {
				continue
			}
			detail := run.Error
			if detail == "" {
				detail = strings.Join(run.FailedAssertions, ", ")
			}
			_, _ = fmt.Fprintf(writer, "  - run %d: %s\n", run.Index, detail)
		}
	}
}

func (s *Summary) evaluateBudgets(lifecyclePassed bool) {
	s.BudgetFailures = 0
	s.BudgetAssertions = s.BudgetAssertions[:0]
	s.addBudget(BudgetStartupReadyP95, s.budgets.StartupReadyP95, s.Timings.StartupReady, lifecyclePassed)
	s.addBudget(BudgetReadinessWithdrawalP95, s.budgets.ReadinessWithdrawalP95, s.Timings.ReadinessWithdrawal, lifecyclePassed)
	s.addBudget(BudgetContainerExitP95, s.budgets.ContainerExitP95, s.Timings.ContainerExit, lifecyclePassed)
}

func (s *Summary) addBudget(name string, limit time.Duration, stats DurationStats, lifecyclePassed bool) {
	if limit <= 0 {
		return
	}
	assertion := BudgetAssertion{
		Name:          name,
		Samples:       stats.Samples,
		ObservedP95MS: stats.P95MS,
		LimitMS:       limit.Milliseconds(),
	}
	assertion.Evaluated = lifecyclePassed && stats.Samples == s.RunsRequested
	if !assertion.Evaluated {
		assertion.Message = fmt.Sprintf("not evaluated because %d/%d runs produced passing timing samples", stats.Samples, s.RunsRequested)
		s.BudgetAssertions = append(s.BudgetAssertions, assertion)
		return
	}
	assertion.Passed = assertion.ObservedP95MS <= assertion.LimitMS
	assertion.Message = fmt.Sprintf(
		"observed p95 %s across %d runs; budget %s",
		time.Duration(assertion.ObservedP95MS)*time.Millisecond,
		assertion.Samples,
		time.Duration(assertion.LimitMS)*time.Millisecond,
	)
	if !assertion.Passed {
		s.BudgetFailures++
	}
	s.BudgetAssertions = append(s.BudgetAssertions, assertion)
}

func calculateStats(values []int64) DurationStats {
	if len(values) == 0 {
		return DurationStats{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return DurationStats{
		Samples: len(sorted),
		MinMS:   sorted[0],
		P50MS:   nearestRank(sorted, 0.50),
		P95MS:   nearestRank(sorted, 0.95),
		MaxMS:   sorted[len(sorted)-1],
	}
}

func nearestRank(sorted []int64, percentile float64) int64 {
	index := int(math.Ceil(percentile*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}

func writeTiming(writer io.Writer, name string, value DurationStats) {
	_, _ = fmt.Fprintf(
		writer,
		"  %-22s min %s  p50 %s  p95 %s  max %s\n",
		name+":",
		time.Duration(value.MinMS)*time.Millisecond,
		time.Duration(value.P50MS)*time.Millisecond,
		time.Duration(value.P95MS)*time.Millisecond,
		time.Duration(value.MaxMS)*time.Millisecond,
	)
}
