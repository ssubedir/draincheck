package runtime

import (
	"bytes"
	"context"
	"os/exec"
)

type commandResult struct {
	stdout []byte
	stderr []byte
	err    error
}

type commandRunner interface {
	Run(ctx context.Context, binary string, limit int64, args ...string) commandResult
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, binary string, limit int64, args ...string) commandResult {
	// The runtime binary and arguments are the purpose of this internal process adapter.
	command := exec.CommandContext(ctx, binary, args...) // #nosec G204 G702 -- Inputs come from validated runtime operations.
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: limit}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	return commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), err: err}
}

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int64
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - int64(b.buffer.Len())
	if remaining > 0 {
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		_, _ = b.buffer.Write(data)
	}
	return original, nil
}

func (b *cappedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}
