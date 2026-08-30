package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCreateArgsAreDeterministicAndNotShellJoined(t *testing.T) {
	args := createArgs(ContainerSpec{
		Image:          "example:test; echo unsafe",
		Name:           "draincheck-test",
		RunID:          "abc123",
		ContainerPorts: []int{8080, 50051, 8080},
		Environment: map[string]string{
			"ZED":   "last",
			"ALPHA": "first value",
		},
		HostAliases: map[string]string{
			"host.draincheck.internal": "host-gateway",
		},
	})
	want := []string{
		"create", "--name", "draincheck-test",
		"--label", "io.draincheck.run=abc123",
		"--publish", "127.0.0.1::8080",
		"--publish", "127.0.0.1::50051",
		"--add-host", "host.draincheck.internal:host-gateway",
		"--env", "ALPHA=first value",
		"--env", "ZED=last",
		"example:test; echo unsafe",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %#v\nwant = %#v", args, want)
	}
}

func TestParseHostPort(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int
	}{
		{"127.0.0.1:49153\n", 49153},
		{"[::1]:49154", 49154},
		{"0.0.0.0:49155\n:::49155", 49155},
	} {
		got, err := parseHostPort(test.input)
		if err != nil {
			t.Errorf("parseHostPort(%q): %v", test.input, err)
			continue
		}
		if got != test.want {
			t.Errorf("parseHostPort(%q) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestRemoveArgsUseImmediateForcedCleanupWithPodman(t *testing.T) {
	for _, test := range []struct {
		name        string
		runtimeName string
		force       bool
		want        []string
	}{
		{
			name:        "podman force",
			runtimeName: "podman",
			force:       true,
			want:        []string{"rm", "--force", "--time", "0", "container-id"},
		},
		{
			name:        "docker force",
			runtimeName: "docker",
			force:       true,
			want:        []string{"rm", "--force", "container-id"},
		},
		{
			name:        "podman graceful",
			runtimeName: "podman",
			want:        []string{"rm", "container-id"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := removeArgs(test.runtimeName, "container-id", test.force)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("args = %#v\nwant = %#v", got, test.want)
			}
		})
	}
}

func TestCappedBufferBoundsOutput(t *testing.T) {
	buffer := &cappedBuffer{limit: 4}
	written, err := buffer.Write([]byte("abcdefgh"))
	if err != nil || written != 8 {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if got := string(buffer.Bytes()); got != "abcd" {
		t.Fatalf("buffer = %q", got)
	}
}

func TestInspectRejectsMalformedRuntimeState(t *testing.T) {
	runner := &staticRunner{result: commandResult{stdout: []byte("not-json")}}
	runtime := &CLI{name: "docker", binary: "docker", runner: runner}

	_, err := runtime.Inspect(context.Background(), "container-id")
	if err == nil || !strings.Contains(err.Error(), "decode container state") {
		t.Fatalf("Inspect error = %v, want malformed state error", err)
	}
	want := []string{"container", "inspect", "--format", "{{json .State}}", "container-id"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("args = %#v\nwant = %#v", runner.args, want)
	}
}

func TestWaitUsesBlockingRuntimeCommand(t *testing.T) {
	runner := &staticRunner{}
	runtime := &CLI{name: "podman", binary: "podman", runner: runner}

	if err := runtime.Wait(context.Background(), "container-id"); err != nil {
		t.Fatal(err)
	}
	want := []string{"wait", "container-id"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("args = %#v\nwant = %#v", runner.args, want)
	}
}

func TestWaitReturnsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &staticRunner{result: commandResult{err: errors.New("process killed")}}
	runtime := &CLI{name: "docker", binary: "docker", runner: runner}

	if err := runtime.Wait(ctx, "container-id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context cancellation", err)
	}
}

func TestWaitReportsRuntimeFailure(t *testing.T) {
	runner := &staticRunner{result: commandResult{stderr: []byte("container disappeared"), err: errors.New("exit status 1")}}
	runtime := &CLI{name: "docker", binary: "docker", runner: runner}

	err := runtime.Wait(context.Background(), "container-id")
	if err == nil || !strings.Contains(err.Error(), "wait for container: container disappeared") {
		t.Fatalf("Wait error = %v, want runtime failure", err)
	}
}

func TestExecRunsArgumentVectorInsideContainer(t *testing.T) {
	runner := &staticRunner{result: commandResult{stdout: []byte("healthy\n")}}
	runtime := &CLI{name: "docker", binary: "docker", runner: runner}

	result, err := runtime.Exec(context.Background(), "container-id", []string{"/app/health check", "--ready;false"}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || string(result.Stdout) != "healthy\n" {
		t.Fatalf("exec result = %#v", result)
	}
	want := []string{"exec", "container-id", "/app/health check", "--ready;false"}
	if !reflect.DeepEqual(runner.args, want) {
		t.Fatalf("args = %#v\nwant = %#v", runner.args, want)
	}
}

func TestExecReturnsCommandExitAsResult(t *testing.T) {
	runner := &staticRunner{result: commandResult{stderr: []byte("not ready"), err: exitStatusError(7)}}
	runtime := &CLI{name: "podman", binary: "podman", runner: runner}

	result, err := runtime.Exec(context.Background(), "container-id", []string{"/healthcheck"}, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 || string(result.Stderr) != "not ready" {
		t.Fatalf("exec result = %#v", result)
	}
}

func TestExecReturnsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &staticRunner{result: commandResult{err: errors.New("process killed")}}
	runtime := &CLI{name: "docker", binary: "docker", runner: runner}

	_, err := runtime.Exec(ctx, "container-id", []string{"/healthcheck"}, 4096)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("exec error = %v, want context cancellation", err)
	}
}

type exitStatusError int

func (e exitStatusError) Error() string { return "exit status" }
func (e exitStatusError) ExitCode() int { return int(e) }

type staticRunner struct {
	result commandResult
	args   []string
}

func (r *staticRunner) Run(_ context.Context, _ string, _ int64, args ...string) commandResult {
	r.args = append([]string(nil), args...)
	return r.result
}
