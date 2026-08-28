package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type event struct {
	Type    string `json:"type"`
	Success *bool  `json:"success,omitempty"`
	Message string `json:"message,omitempty"`
}

type workResult struct {
	status int
	err    error
}

func main() {
	if os.Getenv("DRAINCHECK_PROTOCOL_VERSION") != "1" {
		fail("unsupported protocol version")
	}
	target := strings.TrimRight(os.Getenv("DRAINCHECK_TARGET_URL"), "/")
	requestID := os.Getenv("DRAINCHECK_REQUEST_ID")
	if target == "" || requestID == "" {
		fail("missing Draincheck target or request ID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := &http.Client{}
	results := make(chan workResult, 1)
	go requestWork(ctx, client, target, requestID, results)

	if err := waitUntilActive(ctx, client, target, requestID); err != nil {
		fail("work did not become active: " + err.Error())
	}
	write(event{Type: "active"})

	result := <-results
	if result.err != nil {
		failed := false
		write(event{Type: "result", Success: &failed, Message: result.err.Error()})
		return
	}
	success := result.status >= 200 && result.status < 400
	write(event{Type: "result", Success: &success, Message: fmt.Sprintf("HTTP status %d", result.status)})
}

func requestWork(ctx context.Context, client *http.Client, target, requestID string, results chan<- workResult) {
	requestURL := target + "/work?delay=2s&id=" + url.QueryEscape(requestID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		results <- workResult{err: err}
		return
	}
	response, err := client.Do(request)
	if err != nil {
		results <- workResult{err: err}
		return
	}
	defer response.Body.Close()
	results <- workResult{status: response.StatusCode}
}

func waitUntilActive(ctx context.Context, client *http.Client, target, requestID string) error {
	activeURL := target + "/active?id=" + url.QueryEscape(requestID)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, activeURL, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func write(value event) {
	if err := json.NewEncoder(os.Stdout).Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
