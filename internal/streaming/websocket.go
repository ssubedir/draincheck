package streaming

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	maxWebSocketMessageBytes = 64 << 10
	maxWebSocketMessages     = 10_000
)

// WebSocketSpec describes one WebSocket connection observed during a lifecycle run.
type WebSocketSpec struct {
	BaseURL         string
	Path            string
	Headers         map[string]string
	Subprotocols    []string
	TerminalMessage string
}

// WebSocketSnapshot is bounded protocol evidence. It excludes the URL, headers, and payloads.
type WebSocketSnapshot struct {
	Established               bool
	Status                    int
	NegotiatedSubprotocol     string
	Messages                  int
	TerminalMessageReceived   bool
	TerminalMessageReceivedAt time.Time
	CloseFrameReceived        bool
	CloseCode                 int
	CloseReason               string
	ClosedAt                  time.Time
	ErrorKind                 string
	Error                     string
}

// WebSocketRun observes one WebSocket connection asynchronously.
type WebSocketRun struct {
	mu            sync.RWMutex
	snapshot      WebSocketSnapshot
	established   chan struct{}
	done          chan struct{}
	establishOnce sync.Once
}

// StartWebSocket dials and reads one bounded WebSocket connection until it closes or is canceled.
func StartWebSocket(ctx context.Context, client *http.Client, spec WebSocketSpec) *WebSocketRun {
	run := &WebSocketRun{
		established: make(chan struct{}),
		done:        make(chan struct{}),
	}
	go run.read(ctx, client, spec)
	return run
}

func (r *WebSocketRun) Done() <-chan struct{} { return r.done }

func (r *WebSocketRun) Active() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot.Established && r.snapshot.ClosedAt.IsZero()
}

func (r *WebSocketRun) Snapshot() WebSocketSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshot
}

// WaitEstablished waits until the opening handshake succeeds or the observation ends.
func (r *WebSocketRun) WaitEstablished(ctx context.Context) (WebSocketSnapshot, bool) {
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

func (r *WebSocketRun) read(ctx context.Context, client *http.Client, spec WebSocketSpec) {
	defer close(r.done)
	headers := make(http.Header, len(spec.Headers))
	for name, value := range spec.Headers {
		headers.Set(name, value)
	}
	connection, response, err := websocket.Dial(ctx, spec.BaseURL+spec.Path, &websocket.DialOptions{
		HTTPClient:   client,
		HTTPHeader:   headers,
		Subprotocols: append([]string(nil), spec.Subprotocols...),
	})
	if response != nil {
		r.update(func(snapshot *WebSocketSnapshot) { snapshot.Status = response.StatusCode })
	}
	if err != nil {
		if ctx.Err() != nil {
			r.fail("canceled", "WebSocket observation was canceled")
		} else if response != nil {
			r.fail("handshake", "WebSocket opening handshake was rejected")
		} else {
			r.fail("transport", "WebSocket connection failed")
		}
		return
	}
	defer func() { _ = connection.CloseNow() }()
	connection.SetReadLimit(maxWebSocketMessageBytes)
	r.update(func(snapshot *WebSocketSnapshot) {
		snapshot.Established = true
		snapshot.NegotiatedSubprotocol = boundString(connection.Subprotocol(), maxEvidenceStringBytes)
		r.establishOnce.Do(func() { close(r.established) })
	})

	for {
		_, message, err := connection.Read(ctx)
		if err != nil {
			if errors.Is(err, websocket.ErrMessageTooBig) {
				r.fail("message_too_large", "WebSocket message exceeded the 64 KiB limit")
				return
			}
			var closeError websocket.CloseError
			if errors.As(err, &closeError) {
				r.update(func(snapshot *WebSocketSnapshot) {
					snapshot.CloseFrameReceived = true
					snapshot.CloseCode = int(closeError.Code)
					snapshot.CloseReason = boundString(closeError.Reason, maxEvidenceStringBytes)
					snapshot.ClosedAt = time.Now()
				})
				return
			}
			if ctx.Err() != nil {
				r.fail("canceled", "WebSocket observation was canceled")
			} else {
				r.fail("transport", "WebSocket connection ended without a close frame")
			}
			return
		}

		tooMany := false
		r.update(func(snapshot *WebSocketSnapshot) {
			snapshot.Messages++
			if snapshot.Messages > maxWebSocketMessages {
				tooMany = true
				return
			}
			if spec.TerminalMessage != "" && string(message) == spec.TerminalMessage {
				snapshot.TerminalMessageReceived = true
				snapshot.TerminalMessageReceivedAt = time.Now()
			}
		})
		if tooMany {
			r.fail("message_limit", "WebSocket connection exceeded the 10000-message limit")
			return
		}
	}
}

func (r *WebSocketRun) fail(kind, message string) {
	r.update(func(snapshot *WebSocketSnapshot) {
		snapshot.ErrorKind = kind
		snapshot.Error = message
		snapshot.ClosedAt = time.Now()
	})
}

func (r *WebSocketRun) update(update func(*WebSocketSnapshot)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	update(&r.snapshot)
}
