package report

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ssubedir/draincheck/internal/config"
)

const (
	DebugBundleSchemaVersion = 1
	redactedValue            = "[REDACTED]"
)

type debugTimeline struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	Image         string           `json:"image"`
	Runtime       string           `json:"runtime"`
	Profile       string           `json:"profile"`
	StartedAt     time.Time        `json:"started_at"`
	DurationMS    int64            `json:"duration_ms"`
	Passed        bool             `json:"passed"`
	Events        []Event          `json:"events"`
	Assertions    []Assertion      `json:"assertions"`
	Traffic       TrafficSummary   `json:"traffic"`
	Streaming     StreamingSummary `json:"streaming"`
	Telemetry     TelemetrySummary `json:"telemetry"`
	Shutdown      ShutdownSummary  `json:"shutdown"`
	Timings       TimingSummary    `json:"timings"`
	ForcedCleanup bool             `json:"forced_cleanup"`
	Retained      string           `json:"retained_container,omitempty"`
}

type debugEntry struct {
	name string
	data []byte
}

// WriteDebugBundle writes an atomic ZIP artifact containing bounded evidence from a verification.
func WriteDebugBundle(path string, cfg config.Config, value *Report) error {
	redactedConfig, scrub := redactDebugConfig(cfg)
	timeline := debugTimeline{
		SchemaVersion: DebugBundleSchemaVersion,
		RunID:         value.RunID,
		Image:         value.Image,
		Runtime:       value.Runtime,
		Profile:       value.Profile,
		StartedAt:     value.StartedAt,
		DurationMS:    value.DurationMS,
		Passed:        value.Passed,
		Events:        redactEvents(value.Events, scrub),
		Assertions:    redactAssertions(value.Assertions, scrub),
		Traffic:       value.Traffic,
		Streaming:     value.Streaming,
		Telemetry:     value.Telemetry,
		Shutdown:      value.Shutdown,
		Timings:       value.Timings,
		ForcedCleanup: value.ForcedCleanup,
		Retained:      value.Retained,
	}
	container := value.Container
	container.Error = scrub(container.Error)

	jsonEntries := []struct {
		name  string
		value any
	}{
		{name: "config.json", value: redactedConfig},
		{name: "timeline.json", value: timeline},
		{name: "runtime-state.json", value: container},
	}
	entries := make([]debugEntry, 0, len(jsonEntries)+1)
	for _, entry := range jsonEntries {
		data, err := debugJSON(entry.name, entry.value)
		if err != nil {
			return err
		}
		entries = append(entries, debugEntry{name: entry.name, data: data})
	}
	entries = append(entries, debugEntry{name: "container.log", data: []byte(scrub(value.Logs))})

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			_ = writer.Close()
			return fmt.Errorf("create debug bundle entry %q: %w", entry.name, err)
		}
		if _, err := file.Write(entry.data); err != nil {
			_ = writer.Close()
			return fmt.Errorf("write debug bundle entry %q: %w", entry.name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close debug bundle: %w", err)
	}
	if err := WriteFile(path, archive.Bytes()); err != nil {
		return fmt.Errorf("write debug bundle: %w", err)
	}
	return nil
}

func debugJSON(name string, value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal debug bundle entry %q: %w", name, err)
	}
	return append(data, '\n'), nil
}

func redactDebugConfig(cfg config.Config) (config.Config, func(string) string) {
	secrets := make(map[string]struct{})
	redacted := cfg
	redacted.Target.Environment = cloneMap(cfg.Target.Environment)
	for name, value := range redacted.Target.Environment {
		if sensitiveEnvironmentName(name) {
			addSecret(secrets, value)
			redacted.Target.Environment[name] = redactedValue
		}
	}
	redacted.Traffic.Request.Headers = cloneMap(cfg.Traffic.Request.Headers)
	for name, value := range redacted.Traffic.Request.Headers {
		addSecret(secrets, value)
		redacted.Traffic.Request.Headers[name] = redactedValue
	}
	if body := cfg.Traffic.Request.BodyBytes(); len(body) > 0 {
		addSecret(secrets, string(body))
	}
	if redacted.Traffic.Request.Body != "" {
		redacted.Traffic.Request.Body = redactedValue
	}
	redacted.Traffic.Command.Environment = cloneMap(cfg.Traffic.Command.Environment)
	for name, value := range redacted.Traffic.Command.Environment {
		addSecret(secrets, value)
		redacted.Traffic.Command.Environment[name] = redactedValue
	}
	redacted.Traffic.GRPC.Metadata = cloneMap(cfg.Traffic.GRPC.Metadata)
	for name, value := range redacted.Traffic.GRPC.Metadata {
		addSecret(secrets, value)
		redacted.Traffic.GRPC.Metadata[name] = redactedValue
	}
	if request := cfg.Traffic.GRPC.RequestBytes(); len(request) > 0 && string(request) != "{}" {
		addSecret(secrets, string(request))
	}
	if redacted.Traffic.GRPC.Request != "" {
		redacted.Traffic.GRPC.Request = redactedValue
	}
	redacted.Streaming.SSE.Headers = cloneMap(cfg.Streaming.SSE.Headers)
	for name, value := range redacted.Streaming.SSE.Headers {
		addSecret(secrets, value)
		redacted.Streaming.SSE.Headers[name] = redactedValue
	}
	redacted.Streaming.WebSocket.Headers = cloneMap(cfg.Streaming.WebSocket.Headers)
	for name, value := range redacted.Streaming.WebSocket.Headers {
		addSecret(secrets, value)
		redacted.Streaming.WebSocket.Headers[name] = redactedValue
	}
	redacted.Streaming.GRPC.Metadata = cloneMap(cfg.Streaming.GRPC.Metadata)
	for name, value := range redacted.Streaming.GRPC.Metadata {
		addSecret(secrets, value)
		redacted.Streaming.GRPC.Metadata[name] = redactedValue
	}
	if request := cfg.Streaming.GRPC.RequestBytes(); len(request) > 0 && string(request) != "{}" {
		addSecret(secrets, string(request))
	}
	if redacted.Streaming.GRPC.Request != "" {
		redacted.Streaming.GRPC.Request = redactedValue
	}

	values := make([]string, 0, len(secrets))
	for value := range secrets {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		return len(values[i]) > len(values[j])
	})
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, value, redactedValue)
	}
	if len(pairs) == 0 {
		return redacted, func(value string) string { return value }
	}
	replacer := strings.NewReplacer(pairs...)
	return redacted, replacer.Replace
}

func cloneMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func addSecret(secrets map[string]struct{}, value string) {
	if value != "" && value != redactedValue {
		secrets[value] = struct{}{}
	}
}

func sensitiveEnvironmentName(name string) bool {
	normalized := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(name))
	for _, marker := range []string{
		"AUTH",
		"CREDENTIAL",
		"PASSWORD",
		"PASSWD",
		"PRIVATE_KEY",
		"SECRET",
		"TOKEN",
		"API_KEY",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func redactEvents(source []Event, scrub func(string) string) []Event {
	result := append([]Event(nil), source...)
	for index := range result {
		result[index].Message = scrub(result[index].Message)
	}
	return result
}

func redactAssertions(source []Assertion, scrub func(string) string) []Assertion {
	result := append([]Assertion(nil), source...)
	for index := range result {
		result[index].Message = scrub(result[index].Message)
	}
	return result
}
