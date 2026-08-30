package readiness

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	containerruntime "github.com/ssubedir/draincheck/internal/runtime"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestHTTPCheckerReportsConfiguredReadiness(t *testing.T) {
	statusCode := http.StatusAccepted
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(statusCode)
	}))
	defer server.Close()
	checker := NewHTTP(server.URL, http.StatusOK)
	defer func() { _ = checker.Close() }()

	observation := checker.Check(context.Background())
	if observation.Ready || observation.Err != nil || observation.Description != "HTTP 202" {
		t.Fatalf("unexpected observation: %#v", observation)
	}
	statusCode = http.StatusOK
	observation = checker.Check(context.Background())
	if !observation.Ready || observation.Err != nil || observation.Description != "HTTP 200" {
		t.Fatalf("unexpected ready observation: %#v", observation)
	}
}

func TestGRPCCheckerUsesStandardHealthStatus(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("example.Service", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	checker, err := NewGRPC(listener.Addr().String(), "example.Service")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = checker.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	observation := checker.Check(ctx)
	if !observation.Ready || observation.Err != nil || observation.Description != "gRPC SERVING" {
		t.Fatalf("unexpected ready observation: %#v", observation)
	}
	healthServer.SetServingStatus("example.Service", healthpb.HealthCheckResponse_NOT_SERVING)
	observation = checker.Check(ctx)
	if observation.Ready || observation.Err != nil || observation.Description != "gRPC NOT_SERVING" {
		t.Fatalf("unexpected withdrawn observation: %#v", observation)
	}
}

func TestGRPCCheckerClassifiesHealthRPCFailure(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	grpcServer := grpc.NewServer()
	go func() { _ = grpcServer.Serve(listener) }()
	defer grpcServer.Stop()

	checker, err := NewGRPC(listener.Addr().String(), "example.Service")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = checker.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	observation := checker.Check(ctx)
	if observation.Ready || observation.Err == nil || observation.Description != "gRPC UNIMPLEMENTED" {
		t.Fatalf("unexpected failure observation: %#v", observation)
	}
}

func TestExecCheckerUsesContainerCommandExitStatus(t *testing.T) {
	executor := &fakeContainerExecutor{result: containerruntime.ExecResult{ExitCode: 0}}
	checker := NewExec(executor, "container-id", []string{"/app/healthcheck", "--ready"})

	observation := checker.Check(context.Background())
	if !observation.Ready || observation.Err != nil || observation.Description != "exec exit 0" {
		t.Fatalf("unexpected ready observation: %#v", observation)
	}
	if executor.containerID != "container-id" || executor.outputLimit != 4<<10 || len(executor.command) != 2 {
		t.Fatalf("exec call = %#v", executor)
	}

	executor.result.ExitCode = 1
	observation = checker.Check(context.Background())
	if observation.Ready || observation.Err != nil || observation.Description != "exec exit 1" {
		t.Fatalf("unexpected withdrawn observation: %#v", observation)
	}
}

func TestExecCheckerClassifiesMissingCommandAndTimeout(t *testing.T) {
	executor := &fakeContainerExecutor{result: containerruntime.ExecResult{ExitCode: 127}}
	checker := NewExec(executor, "container-id", []string{"/missing"})
	observation := checker.Check(context.Background())
	if observation.Ready || !observation.Terminal || observation.Err != nil || observation.Description != "exec command not found (exit 127)" {
		t.Fatalf("unexpected missing-command observation: %#v", observation)
	}

	executor.err = context.DeadlineExceeded
	observation = checker.Check(context.Background())
	if observation.Ready || !observation.Terminal || !errors.Is(observation.Err, context.DeadlineExceeded) || observation.Description != "exec timed out" {
		t.Fatalf("unexpected timeout observation: %#v", observation)
	}
}

type fakeContainerExecutor struct {
	result      containerruntime.ExecResult
	err         error
	containerID string
	command     []string
	outputLimit int64
}

func (f *fakeContainerExecutor) Exec(_ context.Context, containerID string, command []string, outputLimit int64) (containerruntime.ExecResult, error) {
	f.containerID = containerID
	f.command = append([]string(nil), command...)
	f.outputLimit = outputLimit
	return f.result, f.err
}
