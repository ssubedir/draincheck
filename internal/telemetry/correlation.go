package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Correlation identifies the remote parent injected into one traffic request.
type Correlation struct {
	RequestID    int
	TraceID      [16]byte
	ParentSpanID [8]byte
}

func NewCorrelations(count int) ([]Correlation, error) {
	correlations := make([]Correlation, count)
	for index := range correlations {
		correlation := Correlation{RequestID: index + 1}
		if _, err := rand.Read(correlation.TraceID[:]); err != nil {
			return nil, fmt.Errorf("generate trace ID: %w", err)
		}
		if _, err := rand.Read(correlation.ParentSpanID[:]); err != nil {
			return nil, fmt.Errorf("generate parent span ID: %w", err)
		}
		correlations[index] = correlation
	}
	return correlations, nil
}

func (c Correlation) TraceParent() string {
	return "00-" + hex.EncodeToString(c.TraceID[:]) + "-" + hex.EncodeToString(c.ParentSpanID[:]) + "-01"
}

func CorrelationHeaders(correlations []Correlation) func(int) map[string]string {
	byRequest := make(map[int]string, len(correlations))
	for _, correlation := range correlations {
		byRequest[correlation.RequestID] = correlation.TraceParent()
	}
	return func(requestID int) map[string]string {
		traceParent, found := byRequest[requestID]
		if !found {
			return nil
		}
		return map[string]string{"traceparent": traceParent}
	}
}
