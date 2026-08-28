package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

type exitError struct {
	code   int
	err    error
	silent bool
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error {
	return e.err
}

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, build BuildInfo) int {
	command := newRootCommand(stdout, stderr, build)
	command.SetArgs(args)
	err := command.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	var coded *exitError
	if errors.As(err, &coded) {
		if !coded.silent && coded.err != nil {
			fmt.Fprintln(stderr, "error:", coded.err)
		}
		return coded.code
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return 130
	}
	fmt.Fprintln(stderr, "error:", err)
	return 2
}

func newRootCommand(stdout, stderr io.Writer, build BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:           "draincheck",
		Short:         "Test container startup and graceful shutdown in CI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(
		newInitCommand(stdout),
		newValidateCommand(stdout),
		newVerifyCommand(stdout),
		newRepeatCommand(stdout),
		newSuiteCommand(stdout),
		newSchemaCommand(stdout),
		newVersionCommand(stdout, build),
	)
	return root
}

func parseByteSize(value string) (int64, error) {
	text := strings.TrimSpace(strings.ToUpper(value))
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		size   int64
	}{
		{"KIB", 1 << 10},
		{"MIB", 1 << 20},
		{"GIB", 1 << 30},
		{"KB", 1000},
		{"MB", 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"B", 1},
	} {
		if strings.HasSuffix(text, unit.suffix) {
			text = strings.TrimSpace(strings.TrimSuffix(text, unit.suffix))
			multiplier = unit.size
			break
		}
	}
	number, err := strconv.ParseInt(text, 10, 64)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid byte size %q", value)
	}
	if number > (1<<31)/multiplier {
		return 0, fmt.Errorf("byte size %q exceeds 2 GiB", value)
	}
	return number * multiplier, nil
}
