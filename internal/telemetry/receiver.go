package telemetry

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	ProtocolHTTPProtobuf = "http/protobuf"
	GatewayHost          = "host.draincheck.internal"
	RunIDAttribute       = "draincheck.run.id"
	tokenHeader          = "X-Draincheck-Token"
	maxRequestBytes      = 16 << 20
)

type Snapshot struct {
	CorrelatedSpans        int
	MatchedRequests        int
	ExportRequests         int
	MetricDataPoints       int
	MetricExportRequests   int
	RejectedExportRequests int
}

type correlationKey struct {
	requestID    int
	parentSpanID [8]byte
}

type metricObservation struct {
	receivedAt time.Time
	dataPoints int
}

type Receiver struct {
	server   *http.Server
	listener net.Listener
	token    string
	runID    string
	done     chan error
	updates  chan struct{}

	mu                   sync.Mutex
	correlations         map[string]correlationKey
	matched              map[int]map[string]time.Time
	metricObservations   []metricObservation
	exportRequests       int
	metricExportRequests int
	rejected             int
	closeOnce            sync.Once
	closeErr             error
}

func StartReceiver(correlations []Correlation, runID string) (*Receiver, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate OTLP receiver token: %w", err)
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("listen for OTLP/HTTP: %w", err)
	}
	receiver := &Receiver{
		listener:     listener,
		token:        hex.EncodeToString(tokenBytes),
		runID:        runID,
		done:         make(chan error, 1),
		updates:      make(chan struct{}, 1),
		correlations: make(map[string]correlationKey, len(correlations)),
		matched:      make(map[int]map[string]time.Time),
	}
	for _, correlation := range correlations {
		receiver.correlations[string(correlation.TraceID[:])] = correlationKey{
			requestID:    correlation.RequestID,
			parentSpanID: correlation.ParentSpanID,
		}
	}
	receiver.server = &http.Server{
		Handler:           http.HandlerFunc(receiver.handle),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       10 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		serveErr := receiver.server.Serve(listener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		receiver.done <- serveErr
	}()
	return receiver, nil
}

func (r *Receiver) TraceEndpoint() string {
	return r.endpoint("/v1/traces")
}

func (r *Receiver) MetricEndpoint() string {
	return r.endpoint("/v1/metrics")
}

func (r *Receiver) endpoint(path string) string {
	port := r.listener.Addr().(*net.TCPAddr).Port
	return fmt.Sprintf("http://%s:%d%s", GatewayHost, port, path)
}

func (r *Receiver) TraceExporterEnvironment() map[string]string {
	return map[string]string{
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": r.TraceEndpoint(),
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": ProtocolHTTPProtobuf,
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS":  strings.ToLower(tokenHeader) + "=" + r.token,
	}
}

func (r *Receiver) MetricExporterEnvironment(existingResourceAttributes string) map[string]string {
	attributes := RunIDAttribute + "=" + r.runID
	if strings.TrimSpace(existingResourceAttributes) != "" {
		attributes = strings.TrimRight(existingResourceAttributes, ",") + "," + attributes
	}
	return map[string]string{
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": r.MetricEndpoint(),
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": ProtocolHTTPProtobuf,
		"OTEL_EXPORTER_OTLP_METRICS_HEADERS":  strings.ToLower(tokenHeader) + "=" + r.token,
		"OTEL_RESOURCE_ATTRIBUTES":            attributes,
	}
}

func (r *Receiver) WaitForTraces(ctx context.Context, eligibleRequestIDs []int, receivedAfter time.Time, minimum int, timeout time.Duration) (Snapshot, error) {
	return r.wait(ctx, timeout, func() (Snapshot, bool) {
		snapshot := r.Snapshot(eligibleRequestIDs, receivedAfter)
		return snapshot, snapshot.CorrelatedSpans >= minimum
	})
}

func (r *Receiver) WaitForMetrics(ctx context.Context, receivedAfter time.Time, minimum int, timeout time.Duration) (Snapshot, error) {
	return r.wait(ctx, timeout, func() (Snapshot, bool) {
		snapshot := r.Snapshot(nil, receivedAfter)
		return snapshot, snapshot.MetricDataPoints >= minimum
	})
}

func (r *Receiver) wait(ctx context.Context, timeout time.Duration, current func() (Snapshot, bool)) (Snapshot, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		snapshot, complete := current()
		if complete {
			return snapshot, nil
		}
		select {
		case <-r.updates:
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return snapshot, ctx.Err()
			}
			return snapshot, nil
		}
	}
}

func (r *Receiver) Snapshot(eligibleRequestIDs []int, receivedAfter time.Time) Snapshot {
	eligible := make(map[int]struct{}, len(eligibleRequestIDs))
	for _, requestID := range eligibleRequestIDs {
		eligible[requestID] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := Snapshot{
		ExportRequests:         r.exportRequests,
		MetricExportRequests:   r.metricExportRequests,
		RejectedExportRequests: r.rejected,
	}
	for requestID, spans := range r.matched {
		if _, found := eligible[requestID]; !found {
			continue
		}
		matchedRequest := false
		for _, receivedAt := range spans {
			if receivedAt.Before(receivedAfter) {
				continue
			}
			snapshot.CorrelatedSpans++
			matchedRequest = true
		}
		if matchedRequest {
			snapshot.MatchedRequests++
		}
	}
	for _, observation := range r.metricObservations {
		if !observation.receivedAt.Before(receivedAfter) {
			snapshot.MetricDataPoints += observation.dataPoints
		}
	}
	return snapshot
}

func (r *Receiver) Close(ctx context.Context) error {
	r.closeOnce.Do(func() {
		shutdownErr := r.server.Shutdown(ctx)
		if shutdownErr != nil {
			_ = r.server.Close()
		}
		var serveErr error
		select {
		case serveErr = <-r.done:
		case <-ctx.Done():
			serveErr = ctx.Err()
		}
		if shutdownErr != nil {
			r.closeErr = fmt.Errorf("stop OTLP/HTTP receiver: %w", shutdownErr)
		} else if serveErr != nil {
			r.closeErr = fmt.Errorf("serve OTLP/HTTP: %w", serveErr)
		}
	})
	return r.closeErr
}

func (r *Receiver) handle(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || (request.URL.Path != "/v1/traces" && request.URL.Path != "/v1/metrics") {
		r.reject(writer, http.StatusNotFound, "OTLP endpoint not found")
		return
	}
	providedToken := request.Header.Get(tokenHeader)
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(r.token)) != 1 {
		r.reject(writer, http.StatusUnauthorized, "invalid Draincheck receiver token")
		return
	}
	payload, ok := r.readPayload(writer, request)
	if !ok {
		return
	}
	receivedAt := time.Now()
	switch request.URL.Path {
	case "/v1/traces":
		var exportRequest collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(payload, &exportRequest); err != nil {
			r.reject(writer, http.StatusBadRequest, "decode ExportTraceServiceRequest")
			return
		}
		r.recordTraces(&exportRequest, receivedAt)
		r.writeSuccess(writer, &collectortrace.ExportTraceServiceResponse{})
	case "/v1/metrics":
		var exportRequest collectormetric.ExportMetricsServiceRequest
		if err := proto.Unmarshal(payload, &exportRequest); err != nil {
			r.reject(writer, http.StatusBadRequest, "decode ExportMetricsServiceRequest")
			return
		}
		r.recordMetrics(&exportRequest, receivedAt)
		r.writeSuccess(writer, &collectormetric.ExportMetricsServiceResponse{})
	}
}

func (r *Receiver) readPayload(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-protobuf" {
		r.reject(writer, http.StatusUnsupportedMediaType, "Content-Type must be application/x-protobuf")
		return nil, false
	}
	if request.ContentLength > maxRequestBytes {
		r.reject(writer, http.StatusRequestEntityTooLarge, "OTLP request body exceeds 16 MiB")
		return nil, false
	}

	body := io.Reader(http.MaxBytesReader(writer, request.Body, maxRequestBytes))
	if encoding := strings.ToLower(strings.TrimSpace(request.Header.Get("Content-Encoding"))); encoding != "" && encoding != "identity" {
		if encoding != "gzip" {
			r.reject(writer, http.StatusUnsupportedMediaType, "Content-Encoding must be gzip or identity")
			return nil, false
		}
		compressed, err := gzip.NewReader(body)
		if err != nil {
			r.reject(writer, http.StatusBadRequest, "invalid gzip body")
			return nil, false
		}
		defer compressed.Close()
		body = compressed
	}
	payload, err := io.ReadAll(io.LimitReader(body, maxRequestBytes+1))
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			r.reject(writer, http.StatusRequestEntityTooLarge, "OTLP request body exceeds 16 MiB")
			return nil, false
		}
		r.reject(writer, http.StatusBadRequest, "read OTLP request body")
		return nil, false
	}
	if len(payload) > maxRequestBytes {
		r.reject(writer, http.StatusRequestEntityTooLarge, "OTLP request body exceeds 16 MiB")
		return nil, false
	}
	return payload, true
}

func (r *Receiver) recordTraces(request *collectortrace.ExportTraceServiceRequest, receivedAt time.Time) {
	r.mu.Lock()
	r.exportRequests++
	added := false
	for _, resourceSpans := range request.GetResourceSpans() {
		for _, scopeSpans := range resourceSpans.GetScopeSpans() {
			for _, span := range scopeSpans.GetSpans() {
				correlation, found := r.correlations[string(span.GetTraceId())]
				if !found || len(span.GetParentSpanId()) != len(correlation.parentSpanID) || subtle.ConstantTimeCompare(span.GetParentSpanId(), correlation.parentSpanID[:]) != 1 {
					continue
				}
				if len(span.GetSpanId()) != 8 {
					continue
				}
				spans := r.matched[correlation.requestID]
				if spans == nil {
					spans = make(map[string]time.Time)
					r.matched[correlation.requestID] = spans
				}
				spanID := string(span.GetSpanId())
				if _, duplicate := spans[spanID]; duplicate {
					continue
				}
				spans[spanID] = receivedAt
				added = true
			}
		}
	}
	r.mu.Unlock()
	if added {
		r.notify()
	}
}

func (r *Receiver) recordMetrics(request *collectormetric.ExportMetricsServiceRequest, receivedAt time.Time) {
	dataPoints := 0
	for _, resourceMetrics := range request.GetResourceMetrics() {
		if !hasRunID(resourceMetrics.GetResource().GetAttributes(), r.runID) {
			continue
		}
		for _, scopeMetrics := range resourceMetrics.GetScopeMetrics() {
			for _, metric := range scopeMetrics.GetMetrics() {
				dataPoints += metricDataPointCount(metric)
			}
		}
	}
	r.mu.Lock()
	r.metricExportRequests++
	if dataPoints > 0 {
		r.metricObservations = append(r.metricObservations, metricObservation{receivedAt: receivedAt, dataPoints: dataPoints})
	}
	r.mu.Unlock()
	if dataPoints > 0 {
		r.notify()
	}
}

func hasRunID(attributes []*commonpb.KeyValue, runID string) bool {
	for _, attribute := range attributes {
		if attribute.GetKey() == RunIDAttribute && attribute.GetValue().GetStringValue() == runID {
			return true
		}
	}
	return false
}

func metricDataPointCount(metric *metricpb.Metric) int {
	return len(metric.GetGauge().GetDataPoints()) +
		len(metric.GetSum().GetDataPoints()) +
		len(metric.GetHistogram().GetDataPoints()) +
		len(metric.GetExponentialHistogram().GetDataPoints()) +
		len(metric.GetSummary().GetDataPoints())
}

func (r *Receiver) notify() {
	select {
	case r.updates <- struct{}{}:
	default:
	}
}

func (r *Receiver) writeSuccess(writer http.ResponseWriter, response proto.Message) {
	payload, _ := proto.Marshal(response)
	writer.Header().Set("Content-Type", "application/x-protobuf")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(payload)
}

func (r *Receiver) reject(writer http.ResponseWriter, status int, message string) {
	r.mu.Lock()
	r.rejected++
	r.mu.Unlock()
	payload, _ := proto.Marshal(&statuspb.Status{Message: message})
	writer.Header().Set("Content-Type", "application/x-protobuf")
	writer.WriteHeader(status)
	_, _ = writer.Write(payload)
}
