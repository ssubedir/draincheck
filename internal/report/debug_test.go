package report

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ssubedir/draincheck/internal/config"
)

func TestWriteDebugBundleContainsBoundedRedactedEvidence(t *testing.T) {
	cfg := config.Default()
	cfg.Target.Image = "fixture:test"
	cfg.Target.Environment = map[string]string{
		"APP_MODE":  "ci",
		"API_TOKEN": "environment-secret",
	}
	cfg.Traffic.Request.Headers = map[string]string{
		"Authorization": "Bearer header-secret",
		"X-Trace-ID":    "trace-secret",
	}
	cfg.Traffic.Request.Body = `{"payload":"body-secret"}`
	cfg.Traffic.Command.Environment = map[string]string{
		"PROBE_TOKEN": "command-secret",
	}
	cfg.Traffic.GRPC.Request = `{"token":"grpc-request-secret"}`
	cfg.Traffic.GRPC.Metadata = map[string]string{
		"authorization": "grpc-metadata-secret",
	}
	cfg.Streaming.SSE.Headers = map[string]string{
		"Authorization": "Bearer stream-secret",
	}
	cfg.Streaming.WebSocket.Headers = map[string]string{
		"Authorization": "Bearer websocket-secret",
	}
	cfg.Streaming.GRPC.Request = `{"token":"grpc-stream-request-secret"}`
	cfg.Streaming.GRPC.Metadata = map[string]string{
		"authorization": "grpc-stream-metadata-secret",
	}

	started := time.Now()
	value := New("abc123", cfg.Target.Image, "fake", started)
	value.Container = ContainerSummary{
		ID:     "container-id",
		Status: "exited",
		Error:  "runtime repeated environment-secret",
	}
	value.Logs = "token=environment-secret auth=Bearer header-secret trace=trace-secret"
	value.AddEvent(started.Add(time.Second), "failed", "event repeated environment-secret")
	value.AddAssertion("execution.completed", false, "Bearer header-secret was rejected")
	value.Finish(started.Add(2 * time.Second))

	path := filepath.Join(t.TempDir(), "debug.zip")
	if err := WriteDebugBundle(path, cfg, value); err != nil {
		t.Fatal(err)
	}
	entries := readDebugBundle(t, path)
	for _, name := range []string{"config.json", "timeline.json", "runtime-state.json", "container.log"} {
		if _, found := entries[name]; !found {
			t.Errorf("debug bundle is missing %q", name)
		}
	}

	combined := string(entries["config.json"]) + string(entries["timeline.json"]) +
		string(entries["runtime-state.json"]) + string(entries["container.log"])
	for _, secret := range []string{"environment-secret", "Bearer header-secret", "trace-secret", "Bearer stream-secret", "Bearer websocket-secret", "body-secret", "command-secret", "grpc-request-secret", "grpc-metadata-secret", "grpc-stream-request-secret", "grpc-stream-metadata-secret"} {
		if strings.Contains(combined, secret) {
			t.Errorf("debug bundle exposed secret %q", secret)
		}
	}
	if !strings.Contains(combined, redactedValue) {
		t.Fatal("debug bundle contains no redaction markers")
	}

	var bundledConfig config.Config
	if err := json.Unmarshal(entries["config.json"], &bundledConfig); err != nil {
		t.Fatalf("decode bundled config: %v", err)
	}
	if bundledConfig.Target.Environment["APP_MODE"] != "ci" {
		t.Errorf("non-sensitive environment value = %q, want ci", bundledConfig.Target.Environment["APP_MODE"])
	}
	if bundledConfig.Target.Environment["API_TOKEN"] != redactedValue {
		t.Errorf("API_TOKEN = %q, want redacted", bundledConfig.Target.Environment["API_TOKEN"])
	}
	for name, value := range bundledConfig.Traffic.Request.Headers {
		if value != redactedValue {
			t.Errorf("header %s = %q, want redacted", name, value)
		}
	}
	if bundledConfig.Traffic.Request.Body != redactedValue {
		t.Errorf("request body = %q, want redacted", bundledConfig.Traffic.Request.Body)
	}
	for name, value := range bundledConfig.Traffic.Command.Environment {
		if value != redactedValue {
			t.Errorf("command environment %s = %q, want redacted", name, value)
		}
	}
	if bundledConfig.Traffic.GRPC.Request != redactedValue {
		t.Errorf("gRPC request = %q, want redacted", bundledConfig.Traffic.GRPC.Request)
	}
	for name, value := range bundledConfig.Traffic.GRPC.Metadata {
		if value != redactedValue {
			t.Errorf("gRPC metadata %s = %q, want redacted", name, value)
		}
	}
	for name, value := range bundledConfig.Streaming.SSE.Headers {
		if value != redactedValue {
			t.Errorf("streaming header %s = %q, want redacted", name, value)
		}
	}
	for name, value := range bundledConfig.Streaming.WebSocket.Headers {
		if value != redactedValue {
			t.Errorf("WebSocket header %s = %q, want redacted", name, value)
		}
	}
	if bundledConfig.Streaming.GRPC.Request != redactedValue {
		t.Errorf("gRPC stream request = %q, want redacted", bundledConfig.Streaming.GRPC.Request)
	}
	for name, value := range bundledConfig.Streaming.GRPC.Metadata {
		if value != redactedValue {
			t.Errorf("gRPC stream metadata %s = %q, want redacted", name, value)
		}
	}
}

func readDebugBundle(t *testing.T, path string) map[string][]byte {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open debug bundle: %v", err)
	}
	defer archive.Close()
	entries := make(map[string][]byte, len(archive.File))
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open debug bundle entry %q: %v", file.Name, err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			t.Fatalf("read debug bundle entry %q: %v", file.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close debug bundle entry %q: %v", file.Name, closeErr)
		}
		entries[file.Name] = data
	}
	return entries
}

func TestWriteDebugBundleReplacesExistingArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.zip")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Target.Image = "fixture:test"
	value := New("abc123", cfg.Target.Image, "fake", time.Now())
	value.AddAssertion("startup.ready", true, "ready")
	value.Finish(time.Now())
	if err := WriteDebugBundle(path, cfg, value); err != nil {
		t.Fatal(err)
	}
	if entries := readDebugBundle(t, path); len(entries) != 4 {
		t.Fatalf("debug bundle entries = %d, want 4", len(entries))
	}
}
