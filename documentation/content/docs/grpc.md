---
title: gRPC lifecycle verification
description: Exercise unary calls, server streams, and gRPC health across the shutdown boundary.
---

Draincheck can use unary gRPC calls as ordinary in-flight work and can hold one server-streaming RPC
open across the shutdown boundary. Both use plaintext HTTP/2, protobuf descriptors, and JSON
request input. They inherit `target.container_port` unless their own `container_port` is set.

gRPC services may also use the standard Health `Check` RPC for startup and withdrawal instead of
exposing an HTTP readiness endpoint. See the [readiness guide](readiness.md); readiness health calls
do not require reflection or a descriptor set.

## Unary traffic

```yaml
traffic:
  driver: grpc
  container_port: 50051 # optional; defaults to target.container_port
  grpc:
    method: example.jobs.v1.Worker/Run
    request: '{"job_id":"draincheck"}'
    # request_file: ./testdata/run-request.json
    metadata:
      authorization: disposable-ci-token
    # descriptor_set: ./api.protoset
    expected_codes: [OK]
  count: 5
  concurrency: 5
  shutdown_after: 500ms
  request_timeout: 10s
```

`method` uses `package.Service/Method` form; a leading slash is also accepted. The method must be
unary. Draincheck resolves its input and output messages before traffic begins, decodes the request
with protobuf JSON rules, and invokes concurrent calls through one gRPC connection. Empty request
input means `{}`.

`request` and `request_file` are mutually exclusive. File paths are relative to the configuration
file, request input is capped at 1 MiB, response messages are capped at 1 MiB, and payloads are never
included in report evidence. `expected_codes` contains one or more canonical status names such as
`OK`, `NOT_FOUND`, or `UNAVAILABLE`; it defaults to `[OK]`.

The ordinary traffic assertions retain their existing meanings. A call is active immediately before
the RPC begins, must remain active when signal delivery is confirmed, and succeeds only when its
final status is configured. The JSON traffic summary identifies `driver: grpc`; failed assertion
messages group failures by gRPC status.

Post-signal `accept` and `reject` policies also work with unary gRPC. A configured expected status is
accepted, an unexpected server status is rejected, and setup, timeout, cancellation, or transport
failures are invalid evidence rather than an intentional rejection.

## Server-streaming shutdown

```yaml
streaming:
  grpc:
    enabled: true
    container_port: 50051 # optional; defaults to target.container_port
    method: example.jobs.v1.Worker/Watch
    request: '{"job_id":"draincheck"}'
    metadata: {}
    # descriptor_set: ./api.protoset
    minimum_messages: 1
    expected_code: OK
    establish_timeout: 2s
    close_timeout: 5s
```

The method must be server-streaming with one client request. Client-streaming and bidirectional
methods are rejected. Draincheck considers the observation established after `minimum_messages`
responses, up to 10,000. It establishes all enabled stream observations before starting ordinary
traffic.

At the signal request, the stream must still be active. It must then finish after the signal, no
later than `close_timeout`, and with `expected_code`. The close timeout is anchored to the signal
request and cannot exceed `shutdown.deadline`. A timeout cancels the observation and fails
`grpc_stream.closed_gracefully`.

Reports expose only bounded protocol evidence under `streaming.grpc`: enabled and established
state, signal-boundary activity, response count, final status, close timing, verdict, and a
classified error. They do not retain response messages, request JSON, descriptor contents, or
metadata values.

## Separate readiness and gRPC ports

`target.container_port` remains the fallback for every probe. Set `readiness.container_port`,
`traffic.container_port`, or `streaming.grpc.container_port` only when that probe uses another
listener. Draincheck publishes every unique selected port once and maps each one to an independent
random loopback host port. For a common HTTP/gRPC split:

```yaml
target:
  container_port: 8080

readiness:
  path: /ready

traffic:
  driver: grpc
  container_port: 50051

streaming:
  grpc:
    enabled: true
    container_port: 50051
```

Here readiness uses port 8080 while unary and streaming gRPC share port 50051.

## Descriptor sources

When `descriptor_set` is omitted, Draincheck requests the service descriptor and its dependencies
through gRPC server reflection. Both the stable v1 reflection API and the older v1alpha API are
accepted. Reflection must be reachable through the mapped port selected for that gRPC probe.

For services that disable reflection, generate a descriptor set in CI or commit a non-sensitive one:

```bash
protoc --include_imports \
  --descriptor_set_out=api.protoset \
  -I proto proto/example/jobs/v1/worker.proto
```

Descriptor-set paths are relative to `draincheck.yaml` and files are capped at 8 MiB. Include imports
so referenced request and response messages can be constructed without source `.proto` files.

## Metadata, telemetry, and transport limits

Metadata keys must be lowercase gRPC keys. Pseudo-headers and the reserved `grpc-` prefix are
rejected. Configured metadata values and inline requests are redacted from debug bundles.

When trace-flush verification is enabled with unary gRPC traffic, Draincheck injects the same unique
W3C `traceparent` correlation value as outgoing gRPC metadata. The application must extract that
metadata into its trace context for correlated spans to count.

The initial implementation supports plaintext gRPC on a selected published container port. TLS,
custom certificate authorities, mutual TLS, client-streaming, bidirectional streaming, compression
configuration, and per-message payload matching are deferred. A command traffic adapter remains
available when one of those behaviors is required.
