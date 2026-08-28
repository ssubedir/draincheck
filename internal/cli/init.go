package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/ssubedir/draincheck/internal/config"
)

func newInitCommand(stdout io.Writer) *cobra.Command {
	var image string
	var port int
	var output string
	var force bool
	command := &cobra.Command{
		Use:   "init",
		Short: "Write a starter draincheck.yaml",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if port < 1 || port > 65535 {
				return &exitError{code: 2, err: errors.New("port must be between 1 and 65535")}
			}
			content := strings.ReplaceAll(config.Template, "{{IMAGE}}", strconv.Quote(image))
			content = strings.ReplaceAll(content, "{{PORT}}", strconv.Itoa(port))
			flags := os.O_WRONLY | os.O_CREATE
			if force {
				flags |= os.O_TRUNC
			} else {
				flags |= os.O_EXCL
			}
			file, err := os.OpenFile(output, flags, 0o644)
			if err != nil {
				return &exitError{code: 2, err: fmt.Errorf("write %s: %w", output, err)}
			}
			if _, err := io.WriteString(file, content); err != nil {
				file.Close()
				return &exitError{code: 3, err: fmt.Errorf("write %s: %w", output, err)}
			}
			if err := file.Close(); err != nil {
				return &exitError{code: 3, err: fmt.Errorf("close %s: %w", output, err)}
			}
			fmt.Fprintf(stdout, "wrote %s\n", output)
			return nil
		},
	}
	command.Flags().StringVar(&image, "image", "", "default image name (may be overridden by verify)")
	command.Flags().IntVar(&port, "port", 8080, "container HTTP port")
	command.Flags().StringVarP(&output, "output", "o", "draincheck.yaml", "configuration path")
	command.Flags().BoolVar(&force, "force", false, "overwrite an existing configuration")
	return command
}
