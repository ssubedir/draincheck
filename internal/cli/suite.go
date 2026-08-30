package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/ssubedir/draincheck/internal/config"
	"github.com/ssubedir/draincheck/internal/lifecycle"
	containerruntime "github.com/ssubedir/draincheck/internal/runtime"
	"github.com/ssubedir/draincheck/internal/suite"
)

const maxSuiteScenarios = 100

var suiteScenarioNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type suiteScenario struct {
	name       string
	configPath string
	config     config.Config
}

func newSuiteCommand(stdout io.Writer) *cobra.Command {
	var configPaths []string
	var imageFlag string
	var runtimeName string
	var profileName string
	var pullName string
	var reportDirectory string
	var logLimitText string
	var noColor bool

	command := &cobra.Command{
		Use:   "suite [image]",
		Short: "Run multiple lifecycle scenarios against one image",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			_ = noColor
			if len(configPaths) < 2 || len(configPaths) > maxSuiteScenarios {
				return &exitError{code: 2, err: fmt.Errorf("suite requires between 2 and %d --config values", maxSuiteScenarios)}
			}
			if strings.TrimSpace(reportDirectory) == "" {
				return &exitError{code: 2, err: errors.New("report directory must not be empty")}
			}
			if len(args) == 1 && imageFlag != "" {
				return &exitError{code: 2, err: errors.New("supply the image either positionally or with --image, not both")}
			}
			imageOverride := imageFlag
			if len(args) == 1 {
				imageOverride = args[0]
			}
			pull, err := parsePullPolicy(pullName)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			logLimit, err := parseByteSize(logLimitText)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			profile, err := config.ParseProfile(profileName)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			scenarios, image, err := loadSuiteScenarios(configPaths, imageOverride, profile)
			if err != nil {
				return &exitError{code: 2, err: err}
			}

			runtime, err := containerruntime.Resolve(command.Context(), runtimeName)
			if err != nil {
				return &exitError{code: 3, err: err}
			}
			definitions := make([]suite.Definition, 0, len(scenarios))
			for _, scenario := range scenarios {
				definitions = append(definitions, suite.Definition{Name: scenario.name, Config: scenario.configPath})
			}
			started := time.Now()
			summary := suite.New(image, runtime.Name(), definitions, started)
			summary.Profile = string(profile)
			var primaryErr error
			for index, scenario := range scenarios {
				_, _ = fmt.Fprintf(stdout, "Scenario %d/%d %s: ", index+1, len(scenarios), scenario.name)
				result, runErr := lifecycle.Verify(command.Context(), scenario.config, runtime, lifecycle.Options{
					PullPolicy: pull,
					LogLimit:   logLimit,
					Profile:    profile,
				})
				artifactDirectory := ""
				if result != nil {
					artifactDirectory = filepath.Join("scenarios", scenario.name)
					artifactPath := filepath.Join(reportDirectory, artifactDirectory)
					if reportErr := writeReports(
						result,
						scenario.config,
						filepath.Join(artifactPath, "draincheck.json"),
						filepath.Join(artifactPath, "draincheck.xml"),
						filepath.Join(artifactPath, "draincheck-debug.zip"),
					); reportErr != nil && runErr == nil {
						runErr = reportErr
					}
					if result.Passed && runErr == nil {
						_, _ = fmt.Fprintf(stdout, "PASS (%s)\n", time.Duration(result.DurationMS)*time.Millisecond)
					} else {
						_, _ = fmt.Fprintln(stdout)
						result.WriteHuman(stdout)
					}
				} else {
					_, _ = fmt.Fprintln(stdout, "ERROR")
				}
				summary.Add(scenario.name, scenario.configPath, result, runErr, filepath.ToSlash(artifactDirectory))
				if runErr != nil {
					primaryErr = runErr
					break
				}
				// Every scenario tests the same resolved image. Pull at most once.
				pull = containerruntime.PullNever
			}

			summary.Finish(time.Now())
			if err := writeSuiteReports(reportDirectory, summary); err != nil && primaryErr == nil {
				primaryErr = err
			}
			summary.WriteHuman(stdout)
			_, _ = fmt.Fprintf(stdout, "\nEvidence: %s\n", reportDirectory)

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
	command.Flags().StringArrayVarP(&configPaths, "config", "c", nil, "scenario configuration path; repeat for each scenario")
	command.Flags().StringVar(&imageFlag, "image", "", "override target.image in every scenario")
	command.Flags().StringVar(&runtimeName, "runtime", "auto", "container runtime: auto, docker, or podman")
	command.Flags().StringVar(&profileName, "profile", string(config.ProfileGeneric), "lifecycle profile: generic or kubernetes")
	command.Flags().StringVar(&pullName, "pull", "never", "image pull policy for the first scenario: never, missing, or always")
	command.Flags().StringVar(&reportDirectory, "report-dir", "reports/draincheck-suite", "aggregate and per-scenario evidence directory")
	command.Flags().StringVar(&logLimitText, "log-limit", "1MiB", "maximum captured container log size per scenario")
	command.Flags().BoolVar(&noColor, "no-color", false, "disable colored output")
	return command
}

func loadSuiteScenarios(configPaths []string, imageOverride string, profile config.Profile) ([]suiteScenario, string, error) {
	scenarios := make([]suiteScenario, 0, len(configPaths))
	seenNames := make(map[string]string, len(configPaths))
	sharedImage := ""
	for _, configPath := range configPaths {
		name, err := suiteScenarioName(configPath)
		if err != nil {
			return nil, "", err
		}
		key := strings.ToLower(name)
		if existing, found := seenNames[key]; found {
			return nil, "", fmt.Errorf("scenario name %q is duplicated by %q and %q", name, existing, configPath)
		}
		seenNames[key] = configPath

		cfg, err := config.LoadFileWithProfile(configPath, profile)
		if err != nil {
			return nil, "", fmt.Errorf("load scenario %q from %q: %w", name, configPath, err)
		}
		if imageOverride != "" {
			cfg.Target.Image = imageOverride
		}
		if err := cfg.Validate(true); err != nil {
			return nil, "", fmt.Errorf("validate scenario %q from %q: %w", name, configPath, err)
		}
		if sharedImage == "" {
			sharedImage = cfg.Target.Image
		} else if cfg.Target.Image != sharedImage {
			return nil, "", fmt.Errorf("scenario %q uses image %q; every suite scenario must use %q or receive one image override", name, cfg.Target.Image, sharedImage)
		}
		scenarios = append(scenarios, suiteScenario{
			name:       name,
			configPath: filepath.ToSlash(filepath.Clean(configPath)),
			config:     cfg,
		})
	}
	return scenarios, sharedImage, nil
}

func suiteScenarioName(configPath string) (string, error) {
	base := filepath.Base(filepath.Clean(configPath))
	extension := strings.ToLower(filepath.Ext(base))
	if extension != ".yaml" && extension != ".yml" {
		return "", fmt.Errorf("suite config %q must use a .yaml or .yml filename", configPath)
	}
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if !suiteScenarioNamePattern.MatchString(name) {
		return "", fmt.Errorf("suite config %q produces invalid scenario name %q; use 1-64 letters, digits, dots, underscores, or hyphens", configPath, name)
	}
	return name, nil
}

func writeSuiteReports(directory string, value *suite.Summary) error {
	return errors.Join(
		suite.WriteJSON(filepath.Join(directory, "summary.json"), value),
		suite.WriteJUnit(filepath.Join(directory, "summary.xml"), value),
	)
}
