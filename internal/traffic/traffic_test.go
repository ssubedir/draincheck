package traffic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStopAndSnapshotCapturesInflightRequests(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	run := Start(context.Background(), server.Client(), Spec{
		BaseURL:     server.URL,
		Method:      http.MethodGet,
		Path:        "/work",
		Count:       4,
		Concurrency: 4,
		Timeout:     time.Second,
	})
	for index := 0; index < 4; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("only %d requests started", index)
		}
	}
	inflight := run.StopAndSnapshot()
	if len(inflight) != 4 {
		t.Fatalf("inflight = %v", inflight)
	}
	if active := run.ActiveSnapshot(); len(active) != 4 {
		t.Fatalf("active after stop = %v", active)
	}
	close(release)
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("traffic did not finish")
	}
	if active := run.ActiveSnapshot(); len(active) != 0 {
		t.Fatalf("active after completion = %v", active)
	}
	for _, result := range run.Results() {
		if !result.Success {
			t.Errorf("request %d failed: %s", result.ID, fmt.Sprint(result.Error))
		}
	}
}

func TestRequestBodyAndConfiguredSuccessStatus(t *testing.T) {
	requests := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		requests <- request.Method + " " + request.Header.Get("Content-Type") + " " + string(body)
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	run := Start(context.Background(), server.Client(), Spec{
		BaseURL:         server.URL,
		Method:          http.MethodPost,
		Path:            "/jobs",
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            []byte(`{"task":"drain"}`),
		SuccessStatuses: []int{http.StatusAccepted},
		Count:           3,
		Concurrency:     3,
		Timeout:         time.Second,
	})
	waitRun(t, run)
	for range 3 {
		if got := <-requests; got != `POST application/json {"task":"drain"}` {
			t.Errorf("request = %q", got)
		}
	}
	for _, result := range run.Results() {
		if !result.Success || result.Status != http.StatusAccepted {
			t.Errorf("result = %#v", result)
		}
	}
}

func TestConfiguredSuccessStatusRejectsDefaultSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	run := Start(context.Background(), server.Client(), Spec{
		BaseURL:         server.URL,
		Method:          http.MethodGet,
		Path:            "/jobs",
		SuccessStatuses: []int{http.StatusAccepted},
		Count:           1,
		Concurrency:     1,
		Timeout:         time.Second,
	})
	waitRun(t, run)
	result := run.Results()[0]
	if result.Success || result.ErrorKind != "http_status" || result.Error != "HTTP 200" {
		t.Fatalf("result = %#v", result)
	}
}

func TestDefaultSuccessStatusesRemain200Through399(t *testing.T) {
	for _, status := range []int{199, 200, 302, 399, 400} {
		want := status >= 200 && status < 400
		if got := statusAccepted(status, nil); got != want {
			t.Errorf("statusAccepted(%d) = %t, want %t", status, got, want)
		}
	}
}

func waitRun(t *testing.T, run *Run) {
	t.Helper()
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("traffic did not finish")
	}
}

func TestPerRequestHeadersOverrideConfiguredHeaders(t *testing.T) {
	traceParents := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		traceParents <- request.Header.Get("traceparent")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	run := Start(context.Background(), server.Client(), Spec{
		BaseURL:     server.URL,
		Method:      http.MethodGet,
		Path:        "/work",
		Headers:     map[string]string{"Traceparent": "configured"},
		Count:       2,
		Concurrency: 2,
		Timeout:     time.Second,
		HeadersForRequest: func(id int) map[string]string {
			return map[string]string{"traceparent": fmt.Sprintf("generated-%d", id)}
		},
	})
	select {
	case <-run.Done():
	case <-time.After(time.Second):
		t.Fatal("traffic did not finish")
	}
	seen := make(map[string]bool)
	for range 2 {
		seen[<-traceParents] = true
	}
	if !seen["generated-1"] || !seen["generated-2"] || seen["configured"] {
		t.Fatalf("traceparent headers = %#v", seen)
	}
}
