package runtime

import (
	"context"
	"time"
)

type PullPolicy string

const (
	PullNever   PullPolicy = "never"
	PullMissing PullPolicy = "missing"
	PullAlways  PullPolicy = "always"
)

type Runtime interface {
	Name() string
	EnsureImage(ctx context.Context, image string, policy PullPolicy) error
	Create(ctx context.Context, spec ContainerSpec) (Container, error)
	Start(ctx context.Context, id string) error
	HostPort(ctx context.Context, id string, containerPort int) (int, error)
	Exec(ctx context.Context, id string, command []string, outputLimit int64) (ExecResult, error)
	Signal(ctx context.Context, id string, signal string) error
	Wait(ctx context.Context, id string) error
	Inspect(ctx context.Context, id string) (ContainerState, error)
	Logs(ctx context.Context, id string, limit int64) ([]byte, error)
	Remove(ctx context.Context, id string, force bool) error
}

type ContainerSpec struct {
	Image          string
	Name           string
	RunID          string
	ContainerPorts []int
	Environment    map[string]string
	HostAliases    map[string]string
}

type Container struct {
	ID   string
	Name string
}

type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

type ContainerState struct {
	Status     string    `json:"Status"`
	Running    bool      `json:"Running"`
	Paused     bool      `json:"Paused"`
	Restarting bool      `json:"Restarting"`
	OOMKilled  bool      `json:"OOMKilled"`
	Dead       bool      `json:"Dead"`
	PID        int       `json:"Pid"`
	ExitCode   int       `json:"ExitCode"`
	Error      string    `json:"Error"`
	StartedAt  time.Time `json:"StartedAt"`
	FinishedAt time.Time `json:"FinishedAt"`
}
