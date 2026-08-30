package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/ssubedir/draincheck/internal/config"
	"github.com/ssubedir/draincheck/internal/report"
)

func newSchemaCommand(stdout io.Writer) *cobra.Command {
	var output string
	command := &cobra.Command{
		Use:   "schema",
		Short: "Print the Draincheck configuration JSON Schema",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			data, err := config.Schema()
			if err != nil {
				return &exitError{code: 3, err: err}
			}
			if output == "" {
				_, err = stdout.Write(data)
				return err
			}
			if err := report.WriteFile(output, data); err != nil {
				return &exitError{code: 3, err: err}
			}
			_, _ = fmt.Fprintf(stdout, "wrote %s\n", output)
			return nil
		},
	}
	command.Flags().StringVarP(&output, "output", "o", "", "write schema to a file instead of stdout")
	return command
}
