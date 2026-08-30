package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newVersionCommand(stdout io.Writer, build BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build metadata",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			_, _ = fmt.Fprintf(stdout, "draincheck %s (commit %s, built %s)\n", build.Version, build.Commit, build.Date)
		},
	}
}
