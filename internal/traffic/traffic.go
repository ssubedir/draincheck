package traffic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type Spec struct {
	BaseURL           string
	Method            string
	Path              string
	Headers           map[string]string
	Body              []byte
	SuccessStatuses   []int
	Count             int
	Concurrency       int
	Timeout           time.Duration
	HeadersForRequest func(id int) map[string]string
}

type Result struct {
	ID        int           `json:"id"`
	StartedAt time.Time     `json:"started_at"`
	Duration  time.Duration `json:"duration"`
	Status    int           `json:"status,omitempty"`
	GRPCCode  string        `json:"grpc_code,omitempty"`
	Success   bool          `json:"success"`
	ErrorKind string        `json:"error_kind,omitempty"`
	Error     string        `json:"error,omitempty"`
}

type Run struct {
	ctx        context.Context
	client     *http.Client
	spec       Spec
	done       chan struct{}
	started    chan struct{}
	startedOne sync.Once

	mu      sync.Mutex
	stopped bool
	active  map[int]struct{}
	results map[int]Result
	count   int
}

func Start(ctx context.Context, client *http.Client, spec Spec) *Run {
	spec.Body = append([]byte(nil), spec.Body...)
	spec.SuccessStatuses = append([]int(nil), spec.SuccessStatuses...)
	run := &Run{
		ctx:     ctx,
		client:  client,
		spec:    spec,
		done:    make(chan struct{}),
		started: make(chan struct{}),
		active:  make(map[int]struct{}),
		results: make(map[int]Result),
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
				run.finish(id, run.request(id))
			}
		}()
	}
	go func() {
		workers.Wait()
		close(run.done)
	}()
	return run
}

func (r *Run) WaitStarted(ctx context.Context) error {
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
		return errors.New("traffic finished before a request started")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Run) StopAndSnapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	return r.activeSnapshotLocked()
}

func (r *Run) ActiveSnapshot() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeSnapshotLocked()
}

func (r *Run) activeSnapshotLocked() []int {
	ids := make([]int, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (r *Run) Done() <-chan struct{} {
	return r.done
}

func (r *Run) Results() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	results := make([]Result, 0, len(r.results))
	for _, result := range r.results {
		results = append(results, result)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results
}

func (r *Run) StartedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count
}

func (r *Run) begin(id int) bool {
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

func (r *Run) finish(id int, result Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, id)
	r.results[id] = result
}

func (r *Run) request(id int) Result {
	started := time.Now()
	result := Result{ID: id, StartedAt: started}
	ctx, cancel := context.WithTimeout(r.ctx, r.spec.Timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, r.spec.Method, r.spec.BaseURL+r.spec.Path, bytes.NewReader(r.spec.Body))
	if err != nil {
		return failed(result, started, "request", err)
	}
	request.Header.Set("User-Agent", "draincheck/traffic")
	for key, value := range r.spec.Headers {
		request.Header.Set(key, value)
	}
	if r.spec.HeadersForRequest != nil {
		for key, value := range r.spec.HeadersForRequest(id) {
			request.Header.Set(key, value)
		}
	}
	response, err := r.client.Do(request)
	if err != nil {
		kind := "transport"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			kind = "timeout"
		} else if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			kind = "canceled"
		}
		return failed(result, started, kind, err)
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return failed(result, started, "response_body", err)
	}
	result.Status = response.StatusCode
	result.Duration = time.Since(started)
	result.Success = statusAccepted(response.StatusCode, r.spec.SuccessStatuses)
	if !result.Success {
		result.ErrorKind = "http_status"
		result.Error = fmt.Sprintf("HTTP %d", response.StatusCode)
	}
	return result
}

func statusAccepted(status int, configured []int) bool {
	if len(configured) == 0 {
		return status >= 200 && status < 400
	}
	for _, accepted := range configured {
		if status == accepted {
			return true
		}
	}
	return false
}

func failed(result Result, started time.Time, kind string, err error) Result {
	result.Duration = time.Since(started)
	result.ErrorKind = kind
	result.Error = strings.TrimSpace(err.Error())
	if len(result.Error) > 300 {
		result.Error = result.Error[:300] + "…"
	}
	return result
}
