package streaming

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEObservesInitialTerminalAndCleanEOF(t *testing.T) {
	closeStream := make(chan struct{})
	defer func() {
		select {
		case <-closeStream:
		default:
			close(closeStream)
		}
	}()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_, _ = fmt.Fprint(writer, "event: ready\ndata: connected\n\n")
		writer.(http.Flusher).Flush()
		<-closeStream
		_, _ = fmt.Fprint(writer, "event: shutdown\ndata: draining\n\n")
	}))
	defer server.Close()

	run, cancel := startTestSSE(t, server.URL, SSESpec{InitialEvent: "ready", TerminalEvent: "shutdown"})
	defer cancel()
	snapshot, established := waitTestEstablished(t, run)
	if !established || !snapshot.InitialEventReceived || !run.Active() {
		t.Fatalf("establishment snapshot = %#v, active = %t", snapshot, run.Active())
	}
	close(closeStream)
	waitTestDone(t, run)
	snapshot = run.Snapshot()
	if !snapshot.TerminalEventReceived || !snapshot.CleanEOF || snapshot.Events != 2 || snapshot.ErrorKind != "" {
		t.Fatalf("final snapshot = %#v", snapshot)
	}
}

func TestSSECleanEOFWithoutTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "event: ready\ndata: connected\n\n")
	}))
	defer server.Close()

	run, cancel := startTestSSE(t, server.URL, SSESpec{InitialEvent: "ready", TerminalEvent: "shutdown"})
	defer cancel()
	waitTestDone(t, run)
	snapshot := run.Snapshot()
	if !snapshot.Established || snapshot.TerminalEventReceived || !snapshot.CleanEOF {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestSSERejectsEOFBeforeInitialEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "event: other\n\n")
	}))
	defer server.Close()

	run, cancel := startTestSSE(t, server.URL, SSESpec{InitialEvent: "ready"})
	defer cancel()
	waitTestDone(t, run)
	if snapshot, established := run.WaitEstablished(context.Background()); established || snapshot.Established {
		t.Fatalf("snapshot = %#v, established = %t", snapshot, established)
	}
}

func TestSSEDoesNotDispatchEventWithoutData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "event: ready\n\n")
	}))
	defer server.Close()

	run, cancel := startTestSSE(t, server.URL, SSESpec{InitialEvent: "ready"})
	defer cancel()
	waitTestDone(t, run)
	if snapshot := run.Snapshot(); snapshot.Established || snapshot.Events != 0 || !snapshot.CleanEOF {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestSSERejectsInvalidContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{}`)
	}))
	defer server.Close()

	run, cancel := startTestSSE(t, server.URL, SSESpec{InitialEvent: "ready"})
	defer cancel()
	waitTestDone(t, run)
	if snapshot := run.Snapshot(); snapshot.ErrorKind != "content_type" || snapshot.Status != http.StatusOK {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestSSEBoundsOversizedLines(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", strings.Repeat("x", maxSSELineBytes))
	}))
	defer server.Close()

	run, cancel := startTestSSE(t, server.URL, SSESpec{InitialEvent: "ready"})
	defer cancel()
	waitTestDone(t, run)
	if snapshot := run.Snapshot(); snapshot.ErrorKind != "event_too_large" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func startTestSSE(t *testing.T, baseURL string, spec SSESpec) (*SSERun, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	spec.BaseURL = baseURL
	spec.Path = "/"
	return StartSSE(ctx, serverClient(), spec), cancel
}

func serverClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}

func waitTestEstablished(t *testing.T, run *SSERun) (SSESnapshot, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return run.WaitEstablished(ctx)
}

func waitTestDone(t *testing.T, run *SSERun) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE run")
	}
}
