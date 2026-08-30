package grpcprobe

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

const maxStreamMessages = 10_000

type StreamSpec struct {
	Client          *Client
	Call            Call
	Metadata        map[string]string
	MinimumMessages int
}

type StreamSnapshot struct {
	Established bool
	Messages    int
	FinalCode   string
	ClosedAt    time.Time
	ErrorKind   string
	Error       string
}

type StreamRun struct {
	mu            sync.RWMutex
	snapshot      StreamSnapshot
	established   chan struct{}
	done          chan struct{}
	establishOnce sync.Once
}

func StartStream(ctx context.Context, spec StreamSpec) *StreamRun {
	run := &StreamRun{established: make(chan struct{}), done: make(chan struct{})}
	go run.receive(ctx, spec)
	return run
}

func (r *StreamRun) Done() <-chan struct{} { return r.done }

func (r *StreamRun) Active() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.Established && r.snapshot.ClosedAt.IsZero()
}

func (r *StreamRun) Snapshot() StreamSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

func (r *StreamRun) WaitEstablished(ctx context.Context) (StreamSnapshot, bool) {
	if snapshot := r.Snapshot(); snapshot.Established {
		return snapshot, true
	}
	select {
	case <-r.established:
		return r.Snapshot(), true
	case <-r.done:
		snapshot := r.Snapshot()
		return snapshot, snapshot.Established
	case <-ctx.Done():
		return r.Snapshot(), false
	}
}

func (r *StreamRun) receive(ctx context.Context, spec StreamSpec) {
	defer close(r.done)
	if len(spec.Metadata) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(spec.Metadata))
	}
	stream, err := spec.Client.connection.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, spec.Call.fullMethod)
	if err != nil {
		r.fail("transport", "could not start the gRPC stream", status.Code(err))
		return
	}
	if err := stream.SendMsg(proto.Clone(spec.Call.request)); err != nil {
		r.fail("transport", "could not send the gRPC stream request", status.Code(err))
		return
	}
	if err := stream.CloseSend(); err != nil {
		r.fail("transport", "could not finish the gRPC stream request", status.Code(err))
		return
	}
	for {
		message := dynamicpb.NewMessage(spec.Call.method.Output())
		err := stream.RecvMsg(message)
		if err != nil {
			code := status.Code(err)
			if errors.Is(err, io.EOF) {
				code = codes.OK
			}
			r.update(func(snapshot *StreamSnapshot) {
				snapshot.FinalCode = CodeName(code)
				snapshot.ClosedAt = time.Now()
				if ctx.Err() != nil {
					snapshot.ErrorKind = "canceled"
					snapshot.Error = "gRPC stream observation was canceled"
				}
			})
			return
		}
		tooMany := false
		r.update(func(snapshot *StreamSnapshot) {
			snapshot.Messages++
			if snapshot.Messages > maxStreamMessages {
				tooMany = true
				return
			}
			if snapshot.Messages >= spec.MinimumMessages {
				snapshot.Established = true
				r.establishOnce.Do(func() { close(r.established) })
			}
		})
		if tooMany {
			r.fail("message_limit", "gRPC stream exceeded the 10000-message limit", codes.ResourceExhausted)
			return
		}
	}
}

func (r *StreamRun) fail(kind, message string, code codes.Code) {
	r.update(func(snapshot *StreamSnapshot) {
		snapshot.FinalCode = CodeName(code)
		snapshot.ErrorKind = kind
		snapshot.Error = message
		snapshot.ClosedAt = time.Now()
	})
}

func (r *StreamRun) update(update func(*StreamSnapshot)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.snapshot)
}
