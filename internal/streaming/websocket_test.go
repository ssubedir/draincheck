package streaming

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWebSocketObservesTerminalMessageAndCloseFrame(t *testing.T) {
	shutdown := make(chan struct{})
	server := webSocketServer(t, func(ctx context.Context, connection *websocket.Conn) {
		if err := connection.Write(ctx, websocket.MessageText, []byte("connected")); err != nil {
			return
		}
		<-shutdown
		if err := connection.Write(ctx, websocket.MessageText, []byte("shutdown")); err != nil {
			return
		}
		_ = connection.Close(websocket.StatusNormalClosure, "draining")
	}, []string{"draincheck.v1"})
	defer server.Close()

	run, cancel := startTestWebSocket(t, server.URL, WebSocketSpec{
		Headers:         map[string]string{"X-Probe": "enabled"},
		Subprotocols:    []string{"draincheck.v1"},
		TerminalMessage: "shutdown",
	})
	defer cancel()
	snapshot, established := waitWebSocketEstablished(t, run)
	if !established || snapshot.Status != http.StatusSwitchingProtocols || snapshot.NegotiatedSubprotocol != "draincheck.v1" || !run.Active() {
		t.Fatalf("establishment snapshot = %#v, active = %t", snapshot, run.Active())
	}
	close(shutdown)
	waitWebSocketDone(t, run)
	snapshot = run.Snapshot()
	if !snapshot.TerminalMessageReceived || !snapshot.CloseFrameReceived || snapshot.CloseCode != int(websocket.StatusNormalClosure) || snapshot.CloseReason != "draining" || snapshot.Messages != 2 || snapshot.ErrorKind != "" {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
}

func TestWebSocketRecordsCleanCloseWithoutTerminalMessage(t *testing.T) {
	server := webSocketServer(t, func(_ context.Context, connection *websocket.Conn) {
		_ = connection.Close(websocket.StatusNormalClosure, "done")
	}, nil)
	defer server.Close()

	run, cancel := startTestWebSocket(t, server.URL, WebSocketSpec{TerminalMessage: "shutdown"})
	defer cancel()
	waitWebSocketDone(t, run)
	snapshot := run.Snapshot()
	if !snapshot.Established || snapshot.TerminalMessageReceived || !snapshot.CloseFrameReceived || snapshot.CloseCode != 1000 || snapshot.ErrorKind != "" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestWebSocketRecordsApplicationCloseCode(t *testing.T) {
	server := webSocketServer(t, func(_ context.Context, connection *websocket.Conn) {
		_ = connection.Close(websocket.StatusGoingAway, "restart")
	}, nil)
	defer server.Close()

	run, cancel := startTestWebSocket(t, server.URL, WebSocketSpec{})
	defer cancel()
	waitWebSocketDone(t, run)
	if snapshot := run.Snapshot(); snapshot.CloseCode != int(websocket.StatusGoingAway) || snapshot.CloseReason != "restart" || !snapshot.CloseFrameReceived {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestWebSocketRejectsOversizedMessage(t *testing.T) {
	server := webSocketServer(t, func(ctx context.Context, connection *websocket.Conn) {
		_ = connection.Write(ctx, websocket.MessageText, []byte(strings.Repeat("x", maxWebSocketMessageBytes+1)))
	}, nil)
	defer server.Close()

	run, cancel := startTestWebSocket(t, server.URL, WebSocketSpec{})
	defer cancel()
	waitWebSocketDone(t, run)
	if snapshot := run.Snapshot(); snapshot.ErrorKind != "message_too_large" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestWebSocketReportsRejectedHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "denied", http.StatusUnauthorized)
	}))
	defer server.Close()

	run, cancel := startTestWebSocket(t, server.URL, WebSocketSpec{})
	defer cancel()
	waitWebSocketDone(t, run)
	if snapshot := run.Snapshot(); snapshot.Established || snapshot.Status != http.StatusUnauthorized || snapshot.ErrorKind != "handshake" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestWebSocketCancellationStopsObservation(t *testing.T) {
	server := webSocketServer(t, func(ctx context.Context, connection *websocket.Conn) {
		_, _, _ = connection.Read(ctx)
	}, nil)
	defer server.Close()

	run, cancel := startTestWebSocket(t, server.URL, WebSocketSpec{})
	waitWebSocketEstablished(t, run)
	cancel()
	waitWebSocketDone(t, run)
	if snapshot := run.Snapshot(); snapshot.ErrorKind != "canceled" || snapshot.ClosedAt.IsZero() {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func webSocketServer(t *testing.T, serve func(context.Context, *websocket.Conn), subprotocols []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Probe") != "" && request.Header.Get("X-Probe") != "enabled" {
			http.Error(writer, "bad probe header", http.StatusBadRequest)
			return
		}
		connection, err := websocket.Accept(writer, request, &websocket.AcceptOptions{Subprotocols: subprotocols})
		if err != nil {
			return
		}
		defer func() { _ = connection.CloseNow() }()
		serve(request.Context(), connection)
	}))
}

func startTestWebSocket(t *testing.T, baseURL string, spec WebSocketSpec) (*WebSocketRun, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	spec.BaseURL = baseURL
	spec.Path = "/"
	return StartWebSocket(ctx, serverClient(), spec), cancel
}

func waitWebSocketEstablished(t *testing.T, run *WebSocketRun) (WebSocketSnapshot, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, established := run.WaitEstablished(ctx)
	if !established {
		t.Fatalf("WebSocket did not establish: %#v", snapshot)
	}
	return snapshot, established
}

func waitWebSocketDone(t *testing.T, run *WebSocketRun) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for WebSocket run")
	}
}
