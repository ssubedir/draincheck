package grpcprobe

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/ssubedir/draincheck/internal/traffic"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/dynamicpb"
)

type UnarySpec struct {
	Client             *Client
	Call               Call
	Metadata           map[string]string
	MetadataForRequest func(id int) map[string]string
	ExpectedCodes      []codes.Code
	Count              int
	Concurrency        int
	Timeout            time.Duration
}

type UnaryRun struct {
	ctx        context.Context
	spec       UnarySpec
	done       chan struct{}
	started    chan struct{}
	startedOne sync.Once

	mu      sync.Mutex
	stopped bool
	active  map[int]struct{}
	results map[int]traffic.Result
	count   int
}

func StartUnary(ctx context.Context, spec UnarySpec) *UnaryRun {
	spec.Metadata = cloneStrings(spec.Metadata)
	spec.ExpectedCodes = append([]codes.Code(nil), spec.ExpectedCodes...)
	run := &UnaryRun{
		ctx: ctx, spec: spec, done: make(chan struct{}), started: make(chan struct{}),
		active: make(map[int]struct{}), results: make(map[int]traffic.Result),
	}
	jobs := make(chan int, spec.Count)
	for id := 1; id <= spec.Count; id++ {
		jobs <- id
	}
	close(jobs)
	var workers sync.WaitGroup
	workers.Add(spec.Concurrency)
	for range spec.Concurrency {
		go func() {
			defer workers.Done()
			for id := range jobs {
				if !run.begin(id) {
					continue
				}
				run.finish(id, run.invoke(id))
			}
		}()
	}
	go func() {
		workers.Wait()
		close(run.done)
	}()
	return run
}

func (r *UnaryRun) WaitStarted(ctx context.Context) error {
	if r.StartedCount() > 0 {
		return nil
	}
	select {
	case <-r.started:
		return nil
	case <-r.done:
		if r.StartedCount() > 0 {
			return nil
		}
		return errors.New("gRPC traffic finished before a call started")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *UnaryRun) StopAndSnapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	return r.activeSnapshotLocked()
}

func (r *UnaryRun) ActiveSnapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeSnapshotLocked()
}

func (r *UnaryRun) Done() <-chan struct{} { return r.done }

func (r *UnaryRun) Results() []traffic.Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	results := make([]traffic.Result, 0, len(r.results))
	for _, result := range r.results {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results
}

func (r *UnaryRun) StartedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *UnaryRun) begin(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return false
	}
	r.active[id] = struct{}{}
	r.count++
	r.startedOne.Do(func() { close(r.started) })
	return true
}

func (r *UnaryRun) finish(id int, result traffic.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, id)
	r.results[id] = result
}

func (r *UnaryRun) activeSnapshotLocked() []int {
	ids := make([]int, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (r *UnaryRun) invoke(id int) traffic.Result {
	started := time.Now()
	result := traffic.Result{ID: id, StartedAt: started}
	ctx, cancel := context.WithTimeout(r.ctx, r.spec.Timeout)
	defer cancel()
	values := cloneStrings(r.spec.Metadata)
	if r.spec.MetadataForRequest != nil {
		for key, value := range r.spec.MetadataForRequest(id) {
			values[key] = value
		}
	}
	if len(values) > 0 {
		ctx = metadata.NewOutgoingContext(ctx, metadata.New(values))
	}
	request := proto.Clone(r.spec.Call.request)
	response := dynamicpb.NewMessage(r.spec.Call.method.Output())
	err := r.spec.Client.connection.Invoke(ctx, r.spec.Call.fullMethod, request, response)
	code := status.Code(err)
	result.Duration = time.Since(started)
	result.GRPCCode = CodeName(code)
	result.Success = codeAccepted(code, r.spec.ExpectedCodes)
	if result.Success {
		return result
	}
	result.ErrorKind = "grpc_status"
	result.Error = "gRPC " + result.GRPCCode
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.ErrorKind = "timeout"
	} else if errors.Is(ctx.Err(), context.Canceled) && code == codes.Canceled {
		result.ErrorKind = "canceled"
	}
	return result
}

func cloneStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
