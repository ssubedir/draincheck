---
title: Troubleshooting Draincheck failures
description: Diagnose lifecycle failures from assertions, event timelines, reports, and bounded debug evidence.
---

Draincheck failures are easiest to diagnose from the failed assertion, followed by the event
timeline. Capture both the machine-readable report and the bounded debug bundle in CI:

```bash
draincheck verify image:tag \
  --config draincheck.yaml \
  --report-json reports/draincheck.json \
  --report-junit reports/draincheck.xml \
  --debug-bundle reports/draincheck-debug.zip
```

Keep artifact-upload steps configured to run even when Draincheck exits non-zero. Avoid
`--keep-on-failure` in unattended CI because it intentionally leaves the failed container behind.

## Reading the evidence

Start with the failed assertion shown in terminal output or `timeline.json`. Then read timeline
events in elapsed-time order and compare the signal-request, signal-confirmation, readiness, traffic,
and exit events.

The debug ZIP contains:

| File | Evidence |
|---|---|
| `config.json` | The resolved configuration, including an image override and applied defaults. |
| `timeline.json` | Verdict, events, assertions, and aggregate traffic results. |
| `runtime-state.json` | Final status, exit code, OOM state, and runtime error text. |
| `container.log` | Container output, capped by `--log-limit`. |

Request-header values, SSE- and WebSocket-header values, inline request bodies, command environment values, and
values of secret-like target environment variables are replaced with `[REDACTED]`. File-backed body
contents are never embedded in the bundle. Exact configured values are also scrubbed from the other
bundle files. Draincheck
cannot recognize fragments or unrelated secrets written independently by the application, so
protect debug artifacts with the same access controls as CI logs.

If no report or bundle was created, the failure happened before a verification result existed.
Check configuration errors, the selected runtime executable, and `docker info` or `podman info`.
Confirm the host and client fall within the [v0.2 support boundary](support.md), especially when the
runtime points at a remote daemon or publishes ports somewhere other than local loopback.

## Assertion index

| Failed assertion | What it means | Inspect first |
|---|---|---|
| `startup.ready` | Readiness never succeeded, or the container exited before becoming ready. | Startup events, container logs, image entrypoint, selected readiness driver, port, and path or gRPC service. |
| `traffic.inflight_exercised` | No request remained active when signal delivery was confirmed. | Request duration, `traffic.shutdown_after`, and the confirmed in-flight count. |
| `shutdown.signal` | The container exited before Draincheck could request termination. | Container logs and final runtime state. |
| `shutdown.pre_stop` | The configured container exec hook failed or consumed the shared shutdown deadline. | Pre-stop exit code/timing, image contents, and the first termination events. |
| `readiness.withdrawn` | Readiness continued returning its configured success status past the budget. | Signal events, readiness handler, and `readiness_withdrawn_within`. |
| `traffic.failed_requests` | More total requests failed than `max_failed_requests` permits. | HTTP status evidence or command error kinds, the probe's result contract, and shutdown ordering. |
| `traffic.inflight_complete` | A request active at confirmed signal delivery failed or never completed. | Listener shutdown and dependency lifetime. |
| `traffic.post_signal_policy` | One or more new requests after signal delivery did not match the configured accept/reject policy. | `traffic.post_signal`, the post-signal timeline events, and listener shutdown timing. |
| `stream.established` | The SSE endpoint did not produce a valid stream and configured initial event before its budget. | `streaming.sse` status, content type, error kind, path, and initial event name. |
| `stream.active_at_signal` | The established SSE connection closed before Draincheck requested the termination signal. | Stream-handler lifetime and application logs before the signal event. |
| `stream.closed_gracefully` | The stream missed its terminal event, clean EOF, or close deadline after signal request. | `streaming.sse` terminal, EOF, close-timing, and error fields. |
| `websocket.established` | The WebSocket opening handshake failed or exceeded its establishment budget. | Path, handshake status, headers, and offered subprotocols. |
| `websocket.active_at_signal` | The established WebSocket closed before Draincheck requested termination. | Handler lifetime, application logs, and early close code/reason. |
| `websocket.closed_gracefully` | The WebSocket missed its terminal message, expected close frame/code, or close deadline. | `streaming.websocket` message, close, timing, and error fields. |
| `grpc_stream.established` | The server stream could not resolve or did not produce enough initial responses. | Method, reflection/descriptor availability, request JSON, metadata, and `streaming.grpc.error_kind`. |
| `grpc_stream.active_at_signal` | The established server stream ended before Draincheck requested termination. | Handler lifetime and the early final gRPC status. |
| `grpc_stream.closed_gracefully` | The server stream missed its expected final status or close deadline. | `streaming.grpc` response count, final status, close timing, and error fields. |
| `telemetry.spans_exported` | Too few spans correlated to confirmed in-flight requests reached Draincheck after signal delivery. | The `telemetry` summary, tracer-provider shutdown, propagation, and OTLP exporter logs. |
| `telemetry.metrics_exported` | Too few run-correlated metric points reached Draincheck after in-flight work completed. | `telemetry.metrics`, meter-provider shutdown, metric recording, and OTLP exporter logs. |
| `shutdown.deadline` | The container did not exit within the shutdown budget. | PID 1, signal handling, stuck work, and internal shutdown timeouts. |
| `shutdown.exit_code` | The final exit code differed from the declared contract. | `runtime-state.json` and the last application log lines. |
| `shutdown.oom` | The runtime reports that the container was OOM-killed. | Memory limits, peak shutdown allocation, and runtime/host memory events. |
| `shutdown.force_kill` | Draincheck had to force-remove the container. | Earlier shutdown and deadline failures; this is usually a consequence. |
| `execution.completed` | A runtime or internal error prevented a valid lifecycle result. | Assertion message, runtime availability, and daemon logs. |
| `cleanup.completed` | Exact-ID container removal failed. | Runtime permissions and daemon health; remove only the reported container ID. |

Several assertions can fail from one root cause. For example, a lost `SIGTERM` commonly produces
`readiness.withdrawn`, `shutdown.deadline`, and `shutdown.force_kill`. Diagnose the earliest causal
event instead of treating every failed assertion as an independent application bug.

## Pre-stop hook failed or exhausted the grace period

The `shutdown.pre_stop` assertion records the hook exit code, duration, and whether the shared
termination deadline expired. Draincheck invokes the argument vector directly inside the target
container and excludes its bounded stdout/stderr from reports.

Run the same command against the image, using only non-secret arguments, and verify that it is
idempotent and finishes quickly:

```bash
docker run --rm --entrypoint /app/pre-stop image:tag --drain
```

With `--profile kubernetes`, time spent in pre-stop reduces the time left for SIGTERM handling and
request draining. Increase `shutdown.deadline` only when it matches the deployment's declared
grace period; otherwise shorten or remove blocking work from the hook. A non-zero hook still leads
Draincheck to request SIGTERM so later lifecycle evidence remains available.

## PID 1 does not receive or handle SIGTERM

Typical evidence:

- Signal delivery is confirmed, but no application log records the signal.
- Readiness remains healthy.
- The shutdown deadline expires and forced cleanup is required.

Inspect the final image rather than only the source repository:

```bash
docker image inspect image:tag \
  --format '{{json .Config.Entrypoint}} {{json .Config.Cmd}}'
```

Use an exec-form entrypoint so the application becomes PID 1:

```dockerfile
# Avoid: a shell becomes PID 1 and may not forward the signal.
ENTRYPOINT my-server

# Prefer: the server receives SIGTERM directly.
ENTRYPOINT ["/app/my-server"]
```

If a wrapper is necessary, finish it with `exec`:

```sh
#!/bin/sh
set -eu
prepare-runtime-files
exec "$@"
```

Also confirm that the application registers its signal handler before it advertises readiness. A
handler that receives `SIGTERM` but never starts shutdown has the same external symptoms as a broken
entrypoint.

## Readiness is not withdrawn

Typical evidence is a confirmed signal followed by repeated readiness success until
`readiness_withdrawn_within` expires.

Make readiness withdrawal the first application action after receiving the signal:

1. Set an internal `shutting_down` state.
2. Make HTTP readiness return a non-success status such as `503`, or gRPC health return
   `NOT_SERVING`.
3. Stop accepting new work where the server or framework supports it.
4. Continue serving requests that were already active.
5. Exit only after draining and final flushes complete.

Do not wait for database closure or full server shutdown before changing readiness. Conversely,
readiness withdrawal must not immediately tear down the listener or dependencies needed by active
handlers.

Draincheck treats an HTTP non-success response or gRPC health state other than `SERVING` as explicit
withdrawal. A connection refusal after process exit also counts as withdrawn, but the timeline
distinguishes the two cases.

## In-flight work is dropped

Typical evidence:

- `traffic.inflight_exercised` passes, proving the scenario had active work.
- `traffic.inflight_complete` or `traffic.failed_requests` fails.
- Logs show canceled handlers, closed clients, or transport resets around shutdown.

Use this shutdown order:

```text
mark unready
  -> stop accepting new work
  -> wait for active handlers
  -> flush telemetry and buffered output
  -> close databases, queues, and shared clients
  -> exit
```

A common bug is placing dependency cleanup in a signal handler while the HTTP server drains on a
different goroutine. Active handlers then lose the resources they need. Coordinate shutdown through
one owner and close shared dependencies only after the server reports that draining is complete.

If `traffic.inflight_exercised` fails instead, the request completed too quickly to test draining.
Choose a deterministic endpoint that remains active longer or reduce `traffic.shutdown_after`.
Do not weaken the assertion: a green run with no confirmed in-flight work proves only startup and
exit behavior.

For initial `traffic.driver: command` work, `active` must mean the target work is already active—not
merely that the helper process started. In the post-signal phase, it means the acceptance attempt is
in flight and awaiting its outcome. Protocol error kinds beginning with `protocol_` identify malformed,
missing, late, oversized, or out-of-order NDJSON. `command_start`, `exit_code`, and `timeout`
identify executable failures; `probe_result` means the adapter deliberately reported
`success: false`. Run the executable with the documented `DRAINCHECK_*` environment locally and
keep all diagnostics on stderr so stdout contains protocol events only. See
[Command probes](command-probes.md).

## Request body or status does not match

An HTTP response is successful when it matches `traffic.request.success_statuses`. When that list
is omitted or empty, the default remains HTTP `200`–`399`. An exact configured list replaces the
default; it does not add to it. Therefore `success_statuses: [202]` treats HTTP `200` as a failed
request and records `http_status` evidence.

Use only one body source. Inline `body` is convenient for short non-secret payloads. `body_file` is
resolved relative to the configuration file, making a committed CI fixture portable across working
directories. Set `Content-Type` explicitly when the endpoint requires it. Draincheck replays the
same bytes for every concurrent request and for the optional post-signal phase; it does not expand
templates or inject per-request values into the body.

If the endpoint reports a missing or malformed payload, first run `draincheck validate` from the
same checkout and inspect the config-relative path. Body files are read before the container starts
and must not exceed 1 MiB. Body contents do not appear in reports, so use a disposable test payload
and application-side diagnostics that do not expose secrets.

## Post-signal request policy mismatch

Post-signal requests are a separate traffic phase. They begin after the runtime confirms signal
delivery and the configured `traffic.post_signal.delay` elapses. They reuse
`traffic.request`; their failures never increase the original `traffic.failed_requests` count.

For `policy: accept`, every request must match `traffic.request.success_statuses`; when that list is
omitted, the default is HTTP `200`–`399`. For `policy: reject`, every request must return another
status or fail at the transport layer. Incomplete requests fail either policy. Inspect
`traffic.post_signal` in the JSON report for configured, started, completed, accepted, and rejected
counts.

A mismatch often means the policy delay does not represent the application's intended shutdown
window. For example, a service may withdraw readiness immediately but keep its listener open while
load-balancer endpoint changes propagate. Use `accept` when that window is intentional; use
`reject` when the process must stop new work as soon as shutdown begins. Keep the policy disabled
when neither behavior is a stable application contract.

## SSE stream shutdown failed

SSE verification is a separate, long-lived connection; it does not replace the ordinary traffic
that proves request draining. Draincheck first requires HTTP `200`–`299`, a
`text/event-stream` media type, and the configured `initial_event`. It then records whether that
same connection is active when the signal request begins.

On shutdown, emit the configured `terminal_event`, flush it, and return from the stream handler so
the client observes clean EOF. The close must occur after the signal request and no later than
`streaming.sse.close_timeout`. Closing before the signal fails `stream.active_at_signal`; returning
without the terminal event, ending mid-event, or exceeding the close budget fails
`stream.closed_gracefully`.

Inspect `streaming.sse.error_kind` first. `status` and `content_type` indicate handshake problems;
`event_too_large` means one line exceeded the 64 KiB safety bound; `truncated` means EOF arrived
without a blank event delimiter; and `canceled` usually means the close timeout expired. Event
matching uses the SSE `event:` field, not `data:`. See the [SSE shutdown guide](streaming.md) for the
full contract.

## WebSocket shutdown failed

WebSocket verification starts with the opening handshake. If `websocket.established` fails, inspect
the reported HTTP status, path, required authentication header, and offered subprotocols. The
built-in observer does not send an application subscription message; use a command probe when the
endpoint cannot become active from the handshake alone.

An established connection must remain open until Draincheck requests the signal. An earlier peer
close fails `websocket.active_at_signal` even when it uses code `1000`. During shutdown, send the
configured terminal message, then initiate a close handshake with the configured code. The terminal
message must be a complete payload received after the signal request; a matching message sent during
startup cannot satisfy the assertion.

Inspect `streaming.websocket.error_kind` and the close evidence. `handshake` means the upgrade was
rejected, `transport` means the connection ended without a close frame, `message_too_large` and
`message_limit` identify safety-limit violations, and `canceled` usually means the close timeout
expired. A received close frame can still fail when its code differs from `close_code`, its terminal
message is missing, or it arrives after `close_timeout`. See the
[WebSocket shutdown guide](websocket.md) for the full contract.

## gRPC verification failed

Method preparation happens after readiness and before traffic starts. If preparation fails,
confirm that the method uses `package.Service/Method`, the server exposes reflection v1 or v1alpha,
or `descriptor_set` contains the service and all imports. Generate descriptor sets with
`protoc --include_imports`. Protobuf JSON field names and value forms must match the input message.

For unary traffic, `traffic.failed_requests` groups unexpected final statuses as `gRPC CODE`.
`UNKNOWN`, `UNAVAILABLE`, or `CANCELED` can indicate that the server stopped its HTTP/2 transport
before active handlers returned. Timeouts and transport/setup failures are not treated as valid
post-signal rejection evidence.

For `grpc_stream.established`, the RPC must deliver at least `minimum_messages` before ordinary
traffic begins. A headers-only stream is not established. For `grpc_stream.active_at_signal`, the
stream must remain open until the signal request. For `grpc_stream.closed_gracefully`, inspect
`final_code`, `closed_after_signal`, and `closed_within_timeout`; the final code must exactly match
`expected_code`.

Configured gRPC uses plaintext HTTP/2. If the application exposes gRPC separately, set
`traffic.container_port` for unary traffic and `streaming.grpc.container_port` for the server
stream; both inherit `target.container_port` when omitted. If it requires TLS, client or
bidirectional streaming, or a subscription message after opening, use a
[command traffic adapter](command-probes.md). See the [gRPC lifecycle guide](grpc.md) for limits and
examples.

## Correlated spans were not exported

`telemetry.spans_exported` proves a specific shutdown path rather than merely checking that an
application emits any telemetry. Draincheck injects a unique W3C `traceparent` for each normal
traffic request and counts unique spans only when all of these are true:

- The span uses the injected trace ID and remote parent span ID.
- Its request was still active when runtime signal delivery was confirmed.
- Its OTLP export reached Draincheck after that confirmation.

If `export_requests` is zero, confirm the image has an OTLP/HTTP protobuf trace exporter and honors
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`, and
`OTEL_EXPORTER_OTLP_TRACES_HEADERS`. The application must extract W3C Trace Context from incoming
HTTP requests. Custom or disabled propagators prevent correlation even if unrelated spans export.

If exports arrive but `correlated_spans` remains zero, inspect whether the HTTP server
instrumentation creates a server span whose parent is the inbound context. If
`rejected_export_requests` is non-zero, inspect exporter diagnostics for an unsupported protocol,
invalid payload, oversized batch, or overwritten authentication header.

The tracer provider must shut down only after in-flight handlers complete and before process exit:

```text
mark unready
  -> drain HTTP handlers
  -> force-flush or shut down the tracer provider
  -> close remaining dependencies
  -> exit
```

Increase `flush_timeout` only when the provider is flushing but the CI host is consistently slow.
It does not change the application's exporter timeout or repair a missing provider-shutdown call.

## Final metrics were not exported

`telemetry.metrics_exported` counts metric data points only when the export carries Draincheck's
injected `draincheck.run.id` resource attribute and arrives after every request that was in-flight
at signal confirmation has completed. An ordinary periodic export during draining is deliberately
too early to pass.

If `telemetry.metrics.export_requests` is zero, confirm the image has an OTLP/HTTP protobuf metric
exporter and honors `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`,
`OTEL_EXPORTER_OTLP_METRICS_PROTOCOL`, and `OTEL_EXPORTER_OTLP_METRICS_HEADERS`. Also ensure custom
resource setup preserves the standard `OTEL_RESOURCE_ATTRIBUTES` detector; replacing SDK resources
without merging environment attributes removes the run marker and prevents correlation.

If exports arrive but `data_points` is zero, verify that the exercised request records a metric and
that the meter provider shuts down after HTTP draining. A long periodic-reader interval is safe:
provider shutdown or force-flush should trigger the final export independently of that interval.
Do not move provider shutdown ahead of handler completion merely to make the assertion pass; the
post-work boundary intentionally rejects that ordering.

## Shutdown deadline mismatch

Draincheck starts the shutdown budget when it requests the configured signal. Runtime command
latency between the `requested` and `delivery confirmed` events consumes part of that budget.

Align the nested timeouts with explicit safety margins:

```text
longest expected request or unit of work
  < application graceful-shutdown timeout
  < Draincheck shutdown.deadline
  < deployment platform termination grace period
```

The application timeout must leave time to flush telemetry, close resources, and exit. Draincheck's
deadline should leave the deployment platform enough margin to avoid its forced-kill boundary. Do
not fix a consistently stuck shutdown by increasing every timeout; first determine which timeline
transition stopped progressing.

For an occasional deadline failure, compare multiple bundles for:

- A large or variable signal-confirmation delay.
- One request that regularly approaches `traffic.request_timeout`.
- Shutdown work that scales with queue depth, connection count, or buffered telemetry.
- Runtime or host contention that delays process scheduling.

## Startup, exit-code, and OOM failures

For `startup.ready`, verify that the final image starts without deployment-only dependencies and
binds the declared `readiness.container_port` (or its `target.container_port` fallback). For HTTP,
check the configured path and success status. For gRPC, confirm the server registers
`grpc.health.v1.Health`, the service name matches, and `Check` returns `SERVING`. An early exit is a
startup failure even when its exit code is zero.

For `shutdown.exit_code`, inspect the actual code in `runtime-state.json`. Exit code `143` commonly
indicates termination by `SIGTERM` rather than a handled graceful exit. If a non-zero exit is an
intentional contract, declare it explicitly with `assertions.exit_code`; otherwise fix the shutdown
path rather than accepting the code.

For `shutdown.oom`, inspect runtime and host memory events. Shutdown can temporarily increase memory
use while serializing buffers or flushing telemetry, so compare peak shutdown usage with steady-state
usage and the deployment memory limit.

## Runtime and cleanup errors

`execution.completed` means Draincheck could not finish a valid lifecycle test. Common causes are a
stopped daemon, missing image, lost runtime connection, externally removed container, or insufficient
permissions. Exit code `3` distinguishes this from an application assertion failure.

Draincheck still attempts exact-ID cleanup after execution errors. If `cleanup.completed` also fails,
the original execution error remains the primary cause. Use the container ID in `runtime-state.json`
or the terminal report for targeted investigation; never clean up by a broad name or image pattern.

After an interrupted local run, check for Draincheck resources with:

```bash
docker ps -a --filter label=io.draincheck.run
# or
podman ps -a --filter label=io.draincheck.run
```

Normal runs should leave no matching resources.
