package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ssubedir/draincheck/internal/config"
	"github.com/ssubedir/draincheck/internal/lifecycle"
	"github.com/ssubedir/draincheck/internal/repetition"
	containerruntime "github.com/ssubedir/draincheck/internal/runtime"
)

func newRepeatCommand(stdout io.Writer) *cobra.Command {
	var configPath string
	var imageFlag string
	var runtimeName string
	var profileName string
	var pullName string
	var reportDirectory string
	var logLimitText string
	var runs int
	var noColor bool

	command := &cobra.Command{
		Use:   "repeat [image]",
		Short: "Repeat the lifecycle scenario and summarize timing evidence",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			_ = noColor
			if runs < 2 || runs > 100 {
				return &exitError{code: 2, err: errors.New("runs must be between 2 and 100")}
			}
			if strings.TrimSpace(reportDirectory) == "" {
				return &exitError{code: 2, err: errors.New("report directory must not be empty")}
			}
			if len(args) == 1 && imageFlag != "" {
				return &exitError{code: 2, err: errors.New("supply the image either positionally or with --image, not both")}
			}

			profile, err := config.ParseProfile(profileName)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			cfg, err := config.LoadFileWithProfile(configPath, profile)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			if len(args) == 1 {
				cfg.Target.Image = args[0]
			} else if imageFlag != "" {
				cfg.Target.Image = imageFlag
			}
			if err := cfg.Validate(true); err != nil {
				return &exitError{code: 2, err: err}
			}
			pull, err := parsePullPolicy(pullName)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			logLimit, err := parseByteSize(logLimitText)
			if err != nil {
				return &exitError{code: 2, err: err}
			}

			runtime, err := containerruntime.Resolve(command.Context(), runtimeName)
			if err != nil {
				return &exitError{code: 3, err: err}
			}
			started := time.Now()
			summary := repetition.New(cfg.Target.Image, runtime.Name(), runs, repetition.Budgets{
				StartupReadyP95:        cfg.Repeat.Budgets.StartupReadyP95.Value(),
				ReadinessWithdrawalP95: cfg.Repeat.Budgets.ReadinessWithdrawalP95.Value(),
				ContainerExitP95:       cfg.Repeat.Budgets.ContainerExitP95.Value(),
			}, started)
			summary.Profile = string(profile)
			var primaryErr error
			for index := 1; index <= runs; index++ {
				fmt.Fprintf(stdout, "Run %d/%d: ", index, runs)
				result, runErr := lifecycle.Verify(command.Context(), cfg, runtime, lifecycle.Options{
					PullPolicy: pull,
					LogLimit:   logLimit,
					Profile:    profile,
				})
				artifactDirectory := ""
				if result != nil {
					artifactDirectory = filepath.Join("runs", fmt.Sprintf("run-%03d-%s", index, result.RunID))
					artifactPath := filepath.Join(reportDirectory, artifactDirectory)
					if reportErr := writeReports(
						result,
						cfg,
						filepath.Join(artifactPath, "draincheck.json"),
						filepath.Join(artifactPath, "draincheck.xml"),
						filepath.Join(artifactPath, "draincheck-debug.zip"),
					); reportErr != nil && runErr == nil {
						runErr = reportErr
					}
					if result.Passed && runErr == nil {
						fmt.Fprintf(stdout, "PASS (%s)\n", time.Duration(result.DurationMS)*time.Millisecond)
					} else {
						fmt.Fprintln(stdout)
						result.WriteHuman(stdout)
					}
				} else {
					fmt.Fprintln(stdout, "ERROR")
				}
				summary.Add(index, result, runErr, filepath.ToSlash(artifactDirectory))
				if runErr != nil {
					primaryErr = runErr
					break
				}
				// A repeat sequence measures one resolved local image. Pull at most once.
				pull = containerruntime.PullNever
			}

			summary.Finish(time.Now())
			if err := writeRepeatReports(reportDirectory, summary); err != nil && primaryErr == nil {
				primaryErr = err
			}
			summary.WriteHuman(stdout)
			fmt.Fprintf(stdout, "\nEvidence: %s\n", reportDirectory)

			if primaryErr != nil {
				if errors.Is(primaryErr, context.Canceled) || command.Context().Err() != nil {
					return &exitError{code: 130, err: primaryErr, silent: true}
				}
				return &exitError{code: 3, err: primaryErr}
			}
			if !summary.Passed {
				return &exitError{code: 1, silent: true}
			}
			return nil
		},
	}
	command.Flags().IntVar(&runs, "runs", 3, "number of isolated lifecycle runs (2-100)")
	command.Flags().StringVarP(&configPath, "config", "c", "draincheck.yaml", "configuration path")
	command.Flags().StringVar(&imageFlag, "image", "", "override target.image")
	command.Flags().StringVar(&runtimeName, "runtime", "auto", "container runtime: auto, docker, or podman")
	command.Flags().StringVar(&profileName, "profile", string(config.ProfileGeneric), "lifecycle profile: generic or kubernetes")
	command.Flags().StringVar(&pullName, "pull", "never", "image pull policy for the first run: never, missing, or always")
	command.Flags().StringVar(&reportDirectory, "report-dir", "reports/draincheck-repeat", "aggregate and per-run evidence directory")
	command.Flags().StringVar(&logLimitText, "log-limit", "1MiB", "maximum captured container log size per run")
	command.Flags().BoolVar(&noColor, "no-color", false, "disable colored output")
	return command
}

func writeRepeatReports(directory string, value *repetition.Summary) error {
	return errors.Join(
		repetition.WriteJSON(filepath.Join(directory, "summary.json"), value),
		repetition.WriteJUnit(filepath.Join(directory, "summary.xml"), value),
	)
}
