package telemetry

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestCorrelationsAreUniqueValidTraceParents(t *testing.T) {
	correlations, err := NewCorrelations(4)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for index, correlation := range correlations {
		if correlation.RequestID != index+1 {
			t.Errorf("request ID = %d, want %d", correlation.RequestID, index+1)
		}
		traceParent := correlation.TraceParent()
		if len(traceParent) != 55 || traceParent[:3] != "00-" || traceParent[35] != '-' || traceParent[52:] != "-01" {
			t.Errorf("invalid traceparent %q", traceParent)
		}
		if seen[traceParent] {
			t.Errorf("duplicate traceparent %q", traceParent)
		}
		seen[traceParent] = true
	}
	headers := CorrelationHeaders(correlations)
	if got := headers(2)["traceparent"]; got != correlations[1].TraceParent() {
		t.Fatalf("request 2 traceparent = %q", got)
	}
	if headers(99) != nil {
		t.Fatal("unknown request received correlation headers")
	}
}

func TestReceiverAcceptsGzipAndMatchesEligibleCorrelatedSpans(t *testing.T) {
	correlations := fixedCorrelations(t, 2)
	receiver, err := StartReceiver(correlations, "run-123")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := receiver.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	before := time.Now()
	request := exportRequest(
		spanFor(correlations[0], "0000000000000001"),
		spanFor(correlations[1], "0000000000000002"),
	)
	postExport(t, receiver, request, true, true)

	snapshot, err := receiver.WaitForTraces(context.Background(), []int{2}, before, 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CorrelatedSpans != 1 || snapshot.MatchedRequests != 1 || snapshot.ExportRequests != 1 || snapshot.RejectedExportRequests != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if lateSnapshot := receiver.Snapshot([]int{2}, time.Now().Add(time.Nanosecond)); lateSnapshot.CorrelatedSpans != 0 {
		t.Fatalf("span exported before the boundary was counted: %#v", lateSnapshot)
	}
}

func TestReceiverRejectsUnauthorizedAndIgnoresWrongParent(t *testing.T) {
	correlations := fixedCorrelations(t, 1)
	receiver, err := StartReceiver(correlations, "run-123")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := receiver.Close(ctx); err != nil {
			t.Error(err)
		}
	}()

	postExport(t, receiver, exportRequest(spanFor(correlations[0], "0000000000000001")), false, false)
	wrongParent := correlations[0]
	wrongParent.ParentSpanID[0] ^= 0xff
	postExport(t, receiver, exportRequest(spanFor(wrongParent, "0000000000000002")), true, false)

	snapshot := receiver.Snapshot([]int{1}, time.Time{})
	if snapshot.CorrelatedSpans != 0 || snapshot.ExportRequests != 1 || snapshot.RejectedExportRequests != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestReceiverMatchesPostBoundaryMetricDataPointsForRun(t *testing.T) {
	receiver, err := StartReceiver(nil, "run-123")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := receiver.Close(ctx); err != nil {
			t.Error(err)
		}
	})

	boundary := time.Now()
	postMetricExport(t, receiver, metricExportRequest("other-run", 1), true)
	postMetricExport(t, receiver, metricExportRequest("run-123", 2), true)
	snapshot, err := receiver.WaitForMetrics(context.Background(), boundary, 2, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.MetricDataPoints != 2 || snapshot.MetricExportRequests != 2 || snapshot.RejectedExportRequests != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if early := receiver.Snapshot(nil, time.Now().Add(time.Nanosecond)); early.MetricDataPoints != 0 {
		t.Fatalf("metric export before boundary was counted: %#v", early)
	}
}

func TestMetricExporterEnvironmentPreservesResourceAttributes(t *testing.T) {
	receiver, err := StartReceiver(nil, "run-123")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := receiver.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	environment := receiver.MetricExporterEnvironment("service.namespace=payments")
	if got := environment["OTEL_RESOURCE_ATTRIBUTES"]; got != "service.namespace=payments,draincheck.run.id=run-123" {
		t.Fatalf("OTEL_RESOURCE_ATTRIBUTES = %q", got)
	}
	if !strings.HasSuffix(environment["OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"], "/v1/metrics") {
		t.Fatalf("metrics endpoint = %q", environment["OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"])
	}
}

func TestReceiverRejectsOversizedBodyWithContentTooLarge(t *testing.T) {
	receiver := &Receiver{token: "test-token"}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/traces", bytes.NewReader(nil))
	request.ContentLength = maxRequestBytes + 1
	request.Header.Set("Content-Type", "application/x-protobuf")
	request.Header.Set(tokenHeader, receiver.token)
	response := httptest.NewRecorder()

	receiver.handle(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
	if receiver.rejected != 1 {
		t.Fatalf("rejected requests = %d, want 1", receiver.rejected)
	}
}

func fixedCorrelations(t *testing.T, count int) []Correlation {
	t.Helper()
	correlations := make([]Correlation, count)
	for index := range correlations {
		traceID, err := hex.DecodeString(fmt.Sprintf("%032x", index+1))
		if err != nil {
			t.Fatal(err)
		}
		parentID, err := hex.DecodeString(fmt.Sprintf("%016x", index+1))
		if err != nil {
			t.Fatal(err)
		}
		correlations[index].RequestID = index + 1
		copy(correlations[index].TraceID[:], traceID)
		copy(correlations[index].ParentSpanID[:], parentID)
	}
	return correlations
}

func spanFor(correlation Correlation, spanIDHex string) *tracepb.Span {
	spanID, _ := hex.DecodeString(spanIDHex)
	return &tracepb.Span{
		TraceId:      append([]byte(nil), correlation.TraceID[:]...),
		SpanId:       spanID,
		ParentSpanId: append([]byte(nil), correlation.ParentSpanID[:]...),
		Name:         "fixture work",
	}
}

func exportRequest(spans ...*tracepb.Span) *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
		}},
	}
}

func metricExportRequest(runID string, dataPoints int) *collectormetric.ExportMetricsServiceRequest {
	points := make([]*metricpb.NumberDataPoint, dataPoints)
	for index := range points {
		points[index] = &metricpb.NumberDataPoint{Value: &metricpb.NumberDataPoint_AsInt{AsInt: int64(index + 1)}}
	}
	return &collectormetric.ExportMetricsServiceRequest{ResourceMetrics: []*metricpb.ResourceMetrics{{
		Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{{
			Key: RunIDAttribute,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{
				StringValue: runID,
			}},
		}}},
		ScopeMetrics: []*metricpb.ScopeMetrics{{Metrics: []*metricpb.Metric{{
			Name: "fixture.completed",
			Data: &metricpb.Metric_Sum{Sum: &metricpb.Sum{
				DataPoints:             points,
				AggregationTemporality: metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				IsMonotonic:            true,
			}},
		}}}},
	}}}
}

func postExport(t *testing.T, receiver *Receiver, exportRequest *collectortrace.ExportTraceServiceRequest, authorized, compressed bool) {
	t.Helper()
	payload, err := proto.Marshal(exportRequest)
	if err != nil {
		t.Fatal(err)
	}
	if compressed {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, err := writer.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		payload = buffer.Bytes()
	}
	port := receiver.listener.Addr().(*net.TCPAddr).Port
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/traces", port), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	if compressed {
		request.Header.Set("Content-Encoding", "gzip")
	}
	if authorized {
		request.Header.Set(tokenHeader, receiver.token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	wantStatus := http.StatusOK
	if !authorized {
		wantStatus = http.StatusUnauthorized
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("export status = %d, want %d", response.StatusCode, wantStatus)
	}
}

func postMetricExport(t *testing.T, receiver *Receiver, exportRequest *collectormetric.ExportMetricsServiceRequest, authorized bool) {
	t.Helper()
	payload, err := proto.Marshal(exportRequest)
	if err != nil {
		t.Fatal(err)
	}
	port := receiver.listener.Addr().(*net.TCPAddr).Port
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/metrics", port), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	if authorized {
		request.Header.Set(tokenHeader, receiver.token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metric export status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}
