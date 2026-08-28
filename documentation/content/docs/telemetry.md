---
title: OpenTelemetry shutdown-flush verification
description: Prove final traces and metrics reach a temporary OTLP receiver before the application exits.
---

Draincheck can prove that traces and metrics created by in-flight HTTP or unary gRPC work reach an OTLP receiver
during shutdown. This catches applications that drain requests correctly but exit before their
OpenTelemetry providers flush final buffered data.

## Configuration

Trace and metric checks are independent and may be enabled together or separately:

```yaml
telemetry:
  traces:
    enabled: true
    minimum_correlated_spans: 1
    flush_timeout: 2s
  metrics:
    enabled: true
    minimum_data_points: 1
    flush_timeout: 2s
```

Trace `minimum_correlated_spans` may be between 1 and 100. Metric `minimum_data_points` may be
between 1 and 10,000. Each `flush_timeout` is how long Draincheck waits for that signal after
normal traffic and container-exit observation; it must be positive and no longer than 30 seconds.

The application image must support the enabled OTLP/HTTP binary protobuf exporters, honor the
standard signal-specific exporter environment variables, and shut down or force-flush its
providers after in-flight work completes and before process exit. Trace verification additionally
requires W3C Trace Context extraction and a span parented by the incoming request context. Metric
verification requires at least one recorded data point from the exercised application work.

OTLP/gRPC, OTLP/JSON, logs, profiles, and a user-supplied collector endpoint are not accepted. The
protocol and default paths follow the
[OpenTelemetry OTLP specification](https://opentelemetry.io/docs/specs/otlp/).

## Correlation and timing model

For traces, Draincheck creates a unique valid `traceparent` for every normal traffic request. HTTP
traffic receives it as a header and gRPC traffic receives it as outgoing metadata. At
confirmed signal delivery it records which request IDs are still active. The receiver counts a
span only when its trace ID and parent span ID match one of those requests and its export arrives
after signal confirmation. Startup spans, readiness spans, unrelated work, and earlier exports
cannot satisfy `telemetry.spans_exported`.

For metrics, Draincheck adds a unique `draincheck.run.id` resource attribute through
`OTEL_RESOURCE_ATTRIBUTES`. The receiver accepts only resource-metric groups carrying that exact
run ID. It then counts data points only when their export arrives after every request that was
in-flight at signal confirmation has completed. This later boundary prevents an ordinary periodic
export during draining from masquerading as the final shutdown flush. Gauge, sum, histogram,
exponential-histogram, and summary data points are counted; raw values and attributes are not
retained.

## Receiver networking and injected environment

Draincheck starts one temporary listener on a random host port and adds this mapping to the test
container:

```text
host.draincheck.internal -> host-gateway
```

For traces, it overrides:

```text
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://host.draincheck.internal:<port>/v1/traces
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_TRACES_HEADERS=x-draincheck-token=<ephemeral-token>
```

For metrics, it overrides:

```text
OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://host.draincheck.internal:<port>/v1/metrics
OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
OTEL_EXPORTER_OTLP_METRICS_HEADERS=x-draincheck-token=<ephemeral-token>
OTEL_RESOURCE_ATTRIBUTES=<existing-attributes>,draincheck.run.id=<run-id>
```

Existing resource attributes are preserved. Exporter variables for disabled signals are unchanged.
The listener accepts only authenticated binary-protobuf requests, supports gzip, limits compressed
and decompressed bodies to 16 MiB, retains aggregate observations rather than telemetry payloads,
and closes at the end of lifecycle execution. The endpoint, run marker, and random 256-bit token
exist only for the temporary test container.

Generic remote daemons remain unsupported because the runtime's host gateway would refer to a
different machine than the Draincheck process. See the [support contract](support.md) for the full
network boundary.

## Reports and CI behavior

When both checks are enabled, JSON and debug timelines include evidence shaped like:

```json
{
  "telemetry": {
    "enabled": true,
    "protocol": "http/protobuf",
    "eligible_inflight_requests": 4,
    "minimum_correlated_spans": 1,
    "correlated_spans": 4,
    "matched_requests": 4,
    "export_requests": 1,
    "rejected_export_requests": 0,
    "metrics": {
      "enabled": true,
      "minimum_data_points": 1,
      "data_points": 1,
      "export_requests": 1
    }
  }
}
```

For report schema v1, the top-level `telemetry.enabled` field remains the trace-verification flag.
Metric-only checks are represented by `telemetry.metrics.enabled` while `telemetry.protocol` still
identifies the receiver protocol.

A trace shortfall fails `telemetry.spans_exported`; a metric shortfall fails
`telemetry.metrics_exported`. Both are emitted as JUnit cases. Raw spans, metric values,
attributes, resource data, receiver endpoints, and credentials are not written to reports.
