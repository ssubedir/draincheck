package streaming

import (
	"bufio"
	"context"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

const maxSSELineBytes = 64 << 10
const maxEvidenceStringBytes = 256

// SSESpec describes the single server-sent event stream observed during a lifecycle run.
type SSESpec struct {
	BaseURL       string
	Path          string
	Headers       map[string]string
	InitialEvent  string
	TerminalEvent string
}

// SSESnapshot is bounded protocol evidence. It deliberately excludes the request URL and headers.
type SSESnapshot struct {
	Established           bool
	Status                int
	ContentType           string
	InitialEventReceived  bool
	TerminalEventReceived bool
	Events                int
	CleanEOF              bool
	ClosedAt              time.Time
	ErrorKind             string
	Error                 string
}

// SSERun observes one SSE connection asynchronously.
type SSERun struct {
	mu            sync.RWMutex
	snapshot      SSESnapshot
	established   chan struct{}
	done          chan struct{}
	establishOnce sync.Once
}

// StartSSE opens and parses one bounded SSE response until EOF or cancellation.
func StartSSE(ctx context.Context, client *http.Client, spec SSESpec) *SSERun {
	run := &SSERun{
		established: make(chan struct{}),
		done:        make(chan struct{}),
	}
	go run.read(ctx, client, spec)
	return run
}

func (r *SSERun) Done() <-chan struct{} { return r.done }

func (r *SSERun) Active() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.Established && r.snapshot.ClosedAt.IsZero()
}

func (r *SSERun) Snapshot() SSESnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

// WaitEstablished waits until the configured initial event is observed or the stream ends.
func (r *SSERun) WaitEstablished(ctx context.Context) (SSESnapshot, bool) {
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

func (r *SSERun) read(ctx context.Context, client *http.Client, spec SSESpec) {
	defer close(r.done)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, spec.BaseURL+spec.Path, nil)
	if err != nil {
		r.fail("request", "could not create the SSE request")
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	for name, value := range spec.Headers {
		req.Header.Set(name, value)
	}

	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			r.fail("canceled", "SSE observation was canceled")
		} else {
			r.fail("transport", "SSE request failed")
		}
		return
	}
	defer func() { _ = response.Body.Close() }()

	contentType := response.Header.Get("Content-Type")
	r.update(func(snapshot *SSESnapshot) {
		snapshot.Status = response.StatusCode
		snapshot.ContentType = boundString(contentType, maxEvidenceStringBytes)
	})
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		r.fail("status", "SSE endpoint returned a non-success status")
		return
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "text/event-stream") {
		r.fail("content_type", "SSE endpoint did not return text/event-stream")
		return
	}

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), maxSSELineBytes)
	eventName := ""
	hasFields := false
	hasData := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if hasFields && hasData {
				r.dispatch(eventName, spec)
			}
			eventName = ""
			hasFields = false
			hasData = false
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if found && strings.HasPrefix(value, " ") {
			value = strings.TrimPrefix(value, " ")
		}
		switch field {
		case "event":
			eventName = value
			hasFields = true
		case "data":
			hasData = true
			hasFields = true
		case "id", "retry":
			hasFields = true
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			r.fail("canceled", "SSE observation was canceled")
		} else if strings.Contains(err.Error(), "token too long") {
			r.fail("event_too_large", "SSE line exceeded the 64 KiB limit")
		} else {
			r.fail("transport", "SSE response ended with a read error")
		}
		return
	}
	if hasFields {
		r.fail("truncated", "SSE response ended before the final event delimiter")
		return
	}
	r.update(func(snapshot *SSESnapshot) {
		snapshot.CleanEOF = true
		snapshot.ClosedAt = time.Now()
	})
}

func (r *SSERun) dispatch(eventName string, spec SSESpec) {
	r.update(func(snapshot *SSESnapshot) {
		snapshot.Events++
		if eventName == spec.InitialEvent {
			snapshot.InitialEventReceived = true
			snapshot.Established = true
			r.establishOnce.Do(func() { close(r.established) })
		}
		if spec.TerminalEvent != "" && eventName == spec.TerminalEvent {
			snapshot.TerminalEventReceived = true
		}
	})
}

func (r *SSERun) fail(kind, message string) {
	r.update(func(snapshot *SSESnapshot) {
		snapshot.ErrorKind = kind
		snapshot.Error = message
		snapshot.ClosedAt = time.Now()
	})
}

func (r *SSERun) update(update func(*SSESnapshot)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.snapshot)
}

func boundString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
