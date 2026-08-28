package runtime

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestExecRunnerStopsCommandWhenContextExpires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := (execRunner{}).Run(
		ctx,
		os.Args[0],
		1024,
		"-test.run=^TestExecRunnerHelperProcess$",
		"--",
		"draincheck-hang",
	)
	if result.err == nil {
		t.Fatal("command unexpectedly completed")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("canceled command took %s", elapsed)
	}
}

func TestExecRunnerHelperProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "draincheck-hang" {
		return
	}
	time.Sleep(30 * time.Second)
}
