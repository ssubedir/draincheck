package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/ssubedir/draincheck/internal/config"
)

func newValidateCommand(stdout io.Writer) *cobra.Command {
	var path string
	var profileName string
	command := &cobra.Command{
		Use:   "validate",
		Short: "Strictly validate configuration without running a container",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			profile, err := config.ParseProfile(profileName)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			cfg, err := config.LoadFileWithProfile(path, profile)
			if err != nil {
				return &exitError{code: 2, err: err}
			}
			if err := cfg.Validate(false); err != nil {
				return &exitError{code: 2, err: err}
			}
			fmt.Fprintf(stdout, "%s is valid\n", path)
			return nil
		},
	}
	command.Flags().StringVarP(&path, "config", "c", "draincheck.yaml", "configuration path")
	command.Flags().StringVar(&profileName, "profile", string(config.ProfileGeneric), "lifecycle profile: generic or kubernetes")
	return command
}
