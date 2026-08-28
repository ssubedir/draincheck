package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/ssubedir/draincheck/internal/config"
	"github.com/ssubedir/draincheck/internal/lifecycle"
	"github.com/ssubedir/draincheck/internal/report"
	containerruntime "github.com/ssubedir/draincheck/internal/runtime"
)

func newVerifyCommand(stdout io.Writer) *cobra.Command {
	var configPath string
	var imageFlag string
	var runtimeName string
	var profileName string
	var pullName string
	var jsonPath string
	var junitPath string
	var debugBundlePath string
	var keepOnFailure bool
	var logLimitText string
	var noColor bool

	command := &cobra.Command{
		Use:   "verify [image]",
		Short: "Execute the container lifecycle scenario",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			_ = noColor
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
			result, runErr := lifecycle.Verify(command.Context(), cfg, runtime, lifecycle.Options{
				PullPolicy:    pull,
				KeepOnFailure: keepOnFailure,
				LogLimit:      logLimit,
				Profile:       profile,
			})
			if result != nil {
				result.WriteHuman(stdout)
				if err := writeReports(result, cfg, jsonPath, junitPath, debugBundlePath); err != nil && runErr == nil {
					runErr = err
				}
			}
			if runErr != nil {
				if errors.Is(runErr, context.Canceled) || command.Context().Err() != nil {
					return &exitError{code: 130, err: runErr, silent: true}
				}
				return &exitError{code: 3, err: runErr}
			}
			if !result.Passed {
				return &exitError{code: 1, silent: true}
			}
			return nil
		},
	}
	command.Flags().StringVarP(&configPath, "config", "c", "draincheck.yaml", "configuration path")
	command.Flags().StringVar(&imageFlag, "image", "", "override target.image")
	command.Flags().StringVar(&runtimeName, "runtime", "auto", "container runtime: auto, docker, or podman")
	command.Flags().StringVar(&profileName, "profile", string(config.ProfileGeneric), "lifecycle profile: generic or kubernetes")
	command.Flags().StringVar(&pullName, "pull", "never", "image pull policy: never, missing, or always")
	command.Flags().StringVar(&jsonPath, "report-json", "", "write a JSON report")
	command.Flags().StringVar(&junitPath, "report-junit", "", "write a JUnit XML report")
	command.Flags().StringVar(&debugBundlePath, "debug-bundle", "", "write a bounded, redacted debug ZIP")
	command.Flags().BoolVar(&keepOnFailure, "keep-on-failure", false, "retain a failed container for debugging")
	command.Flags().StringVar(&logLimitText, "log-limit", "1MiB", "maximum captured container log size")
	command.Flags().BoolVar(&noColor, "no-color", false, "disable colored output")
	return command
}

func parsePullPolicy(value string) (containerruntime.PullPolicy, error) {
	policy := containerruntime.PullPolicy(value)
	switch policy {
	case containerruntime.PullNever, containerruntime.PullMissing, containerruntime.PullAlways:
		return policy, nil
	default:
		return "", fmt.Errorf("pull policy must be never, missing, or always")
	}
}

func writeReports(result *report.Report, cfg config.Config, jsonPath, junitPath, debugBundlePath string) error {
	var problems []error
	if jsonPath != "" {
		if err := report.WriteJSON(jsonPath, result); err != nil {
			problems = append(problems, err)
		}
	}
	if junitPath != "" {
		if err := report.WriteJUnit(junitPath, result); err != nil {
			problems = append(problems, err)
		}
	}
	if debugBundlePath != "" {
		if err := report.WriteDebugBundle(debugBundlePath, cfg, result); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}
