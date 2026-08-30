package readiness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	containerruntime "github.com/ssubedir/draincheck/internal/runtime"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type Checker interface {
	Check(context.Context) Observation
	Close() error
}

type Observation struct {
	Ready       bool
	Terminal    bool
	Description string
	Duration    time.Duration
	Err         error
}

type HTTPChecker struct {
	client        *http.Client
	url           string
	successStatus int
}

func NewHTTP(url string, successStatus int) *HTTPChecker {
	return &HTTPChecker{
		client:        &http.Client{Transport: &http.Transport{DisableKeepAlives: true}},
		url:           url,
		successStatus: successStatus,
	}
}

func (c *HTTPChecker) Check(ctx context.Context) Observation {
	started := time.Now()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Observation{Description: err.Error(), Duration: time.Since(started), Err: err}
	}
	request.Header.Set("User-Agent", "draincheck/readiness")
	response, err := c.client.Do(request)
	if err != nil {
		return Observation{Description: err.Error(), Duration: time.Since(started), Err: err}
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	return Observation{
		Ready:       response.StatusCode == c.successStatus,
		Description: fmt.Sprintf("HTTP %d", response.StatusCode),
		Duration:    time.Since(started),
	}
}

func (c *HTTPChecker) Close() error {
	c.client.CloseIdleConnections()
	return nil
}

type GRPCChecker struct {
	connection *grpc.ClientConn
	client     healthpb.HealthClient
	service    string
}

func NewGRPC(target, service string) (*GRPCChecker, error) {
	connection, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10)),
	)
	if err != nil {
		return nil, fmt.Errorf("create gRPC readiness client: %w", err)
	}
	return &GRPCChecker{
		connection: connection,
		client:     healthpb.NewHealthClient(connection),
		service:    service,
	}, nil
}

func (c *GRPCChecker) Check(ctx context.Context) Observation {
	started := time.Now()
	response, err := c.client.Check(ctx, &healthpb.HealthCheckRequest{Service: c.service})
	if err != nil {
		return Observation{
			Description: "gRPC " + strings.ToUpper(status.Code(err).String()),
			Duration:    time.Since(started),
			Err:         err,
		}
	}
	servingStatus := response.GetStatus()
	return Observation{
		Ready:       servingStatus == healthpb.HealthCheckResponse_SERVING,
		Description: "gRPC " + servingStatus.String(),
		Duration:    time.Since(started),
	}
}

func (c *GRPCChecker) Close() error { return c.connection.Close() }

const execOutputLimit = 4 << 10

type ContainerExecutor interface {
	Exec(context.Context, string, []string, int64) (containerruntime.ExecResult, error)
}

type ExecChecker struct {
	executor    ContainerExecutor
	containerID string
	command     []string
}

func NewExec(executor ContainerExecutor, containerID string, command []string) *ExecChecker {
	return &ExecChecker{
		executor:    executor,
		containerID: containerID,
		command:     append([]string(nil), command...),
	}
}

func (c *ExecChecker) Check(ctx context.Context) Observation {
	started := time.Now()
	result, err := c.executor.Exec(ctx, c.containerID, c.command, execOutputLimit)
	duration := time.Since(started)
	if err != nil {
		description := "exec failed"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			description = "exec timed out"
		case errors.Is(err, context.Canceled):
			description = "exec canceled"
		}
		return Observation{Terminal: true, Description: description, Duration: duration, Err: err}
	}
	description := fmt.Sprintf("exec exit %d", result.ExitCode)
	switch result.ExitCode {
	case 126:
		description = "exec command could not start (exit 126)"
	case 127:
		description = "exec command not found (exit 127)"
	}
	return Observation{
		Ready:       result.ExitCode == 0,
		Terminal:    result.ExitCode == 126 || result.ExitCode == 127,
		Description: description,
		Duration:    duration,
	}
}

func (c *ExecChecker) Close() error { return nil }
