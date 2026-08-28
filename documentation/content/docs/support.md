---
title: Support and stability contract
description: Understand the supported runtimes, platforms, configuration, reports, and compatibility guarantees for v0.2.
---

This contract applies to the `v0.2.x` release line. It defines where maintainers expect Draincheck
to work, which interfaces automation may depend on, and how those interfaces may evolve.

"Supported" means the repository continuously exercises the behavior and treats reproducible
failures within the boundary as Draincheck defects. It does not certify every application,
framework, container image, orchestrator, or host configuration.

## Execution environment

| Area | Supported in v0.2 | Best effort | Not supported in v0.2 |
|---|---|---|---|
| Draincheck binary | Linux `amd64` and `arm64` release archives | Source builds on Windows or macOS | Native Windows/macOS release artifacts |
| Container images under test | Linux OCI/container images for the runner architecture | Emulated architectures configured by the operator | Windows containers |
| Docker | Local Docker Engine 28.0 or newer on Linux | Rootless Docker and Docker Desktop | Generic remote daemons |
| Podman | Local Podman 4.9 or newer on Linux, rootless or rootful | Podman Machine and source builds on Windows/macOS | Generic remote Podman services |
| Network path | Runtime-published random ports bound to `127.0.0.1` | Runtime-managed VM forwarding such as Podman Machine | A daemon host that is not reachable through the local loopback mappings |

Docker remains the first runtime selected by `--runtime auto`; Podman is stable when explicitly
selected or when Docker is unavailable. Draincheck does not reject older clients by version number,
but behavior below the documented floors is best effort and maintainers may request reproduction on
a supported version.

The version floor is evidence-based rather than a claim about every intermediate release:

- CI is fixed to GitHub's Ubuntu 24.04 runner and records the runtime version with every lifecycle
  artifact. The reference image currently provides Docker Engine 28.0.4 and Podman 4.9.3.
- Rootless Podman 6.0.2 through Podman Machine passes the full runtime pilot matrix and the pinned
  external-image pilot.

The runtime client and daemon must act on the same machine, or provide transparent local port
forwarding. Draincheck probes `127.0.0.1` after asking the runtime to publish a random host port; a
generic remote daemon would publish that port on another host.

## Workload protocol

Draincheck supports plaintext HTTP, standard gRPC health, or in-container exec readiness plus
built-in HTTP or gRPC traffic and a host command adapter:

- One readiness check: an HTTP path polled with `GET` and one declared success status, the standard
  unary gRPC Health `Check` RPC with an optional service name and `SERVING` as the ready state, or a
  bounded argument vector executed inside the container with exit `0` as the ready state.
- One HTTP traffic request with a configurable method, path, headers, optional body, and optional
  exact success-status list.
- Alternatively, one trusted host executable per traffic probe using the bounded Draincheck command
  protocol. This lets CI-owned adapters drive queues, gRPC, databases, or another application
  protocol while Draincheck retains lifecycle timing and assertions.
- Alternatively, concurrent unary gRPC traffic resolved through server reflection v1/v1alpha or a
  config-relative descriptor set, with protobuf JSON input, metadata, and exact expected statuses.
- An optional second phase that requires new requests after confirmed signal delivery to be either
  accepted or rejected.
- One optional SSE connection established before traffic, with configurable initial and terminal
  event names and a bounded post-signal close timeout.
- One optional receive-only WebSocket connection established before traffic, with configurable
  headers, subprotocol offers, terminal message, expected close code, and close timeout.
- One optional server-streaming gRPC call established by a minimum response count before traffic,
  with expected final status and a bounded post-signal close timeout.
- Inline and config-relative file request bodies up to 1 MiB. A configured body is replayed
  unchanged for every normal and post-signal request.
- HTTP status codes from `200` through `399` count as successful traffic responses by default. A
  non-empty `success_statuses` list replaces that range with exact codes.
- Post-signal traffic reuses the same request. For that phase, a response matching the configured
  success rule means accepted; another status or a transport failure means rejected.
- Optional trace- and metric-flush verification accepts OTLP/HTTP binary protobuf on `/v1/traces`
  and `/v1/metrics`. OTLP/gRPC, OTLP/JSON, logs, profiles, and arbitrary collector pipelines are not
  supported by this receiver.
- One random loopback host port for each unique configured network probe port. HTTP/gRPC readiness,
  traffic, SSE, WebSocket, and gRPC streaming may select separate container ports and otherwise
  inherit `target.container_port`. Exec readiness publishes no readiness port.
- Linux-style signals accepted by the selected container runtime.

Built-in HTTPS, custom certificate authorities, mutual TLS, TLS-protected gRPC, client- or
bidirectional-streaming gRPC, client-driven WebSocket protocols,
other streaming protocols, queue workers, custom networks, and live-cluster execution are not part
of the current support boundary. A command adapter may implement those workload protocols itself,
and an image-owned exec command may expose readiness without a network endpoint; both remain
application-owned code rather than built-in protocol implementations.

## Configuration version 1

The YAML `version: 1` contract is backward-compatible throughout the v0.x series:

- A configuration accepted by an earlier Draincheck release will continue to be accepted with the
  same field meanings by later v0.x releases.
- Existing fields will not be removed, renamed, or assigned a weaker default within version 1.
- New fields may be added only when omitted configurations preserve existing behavior.
- Unknown fields remain errors. This intentionally catches misspellings and means a configuration
  using a newly added field requires a sufficiently new Draincheck binary.
- A breaking configuration change requires a new configuration version and an explicit migration
  path; it will not be silently reinterpreted as version 1.

The committed JSON Schema is generated from the Go configuration model and published with each
release. Its `$id` points at the repository's `main` branch; release archives contain the exact
schema built from their tagged source.

## Machine-readable output

### JSON report

The JSON report with `schema_version: 1` is the stable automation interface for v0.x:

- Existing field names, JSON types, and meanings are preserved.
- Required v1 fields are not removed.
- New fields may be added. Consumers must ignore unknown fields and branch on `schema_version`.
- A breaking field or semantic change requires a new `schema_version`.
- Timestamps are RFC 3339 JSON strings; elapsed and summary durations use integer milliseconds
  where their field name ends in `_ms`.
- The `timings` object records startup readiness from verification start and signal delivery,
  readiness withdrawal, and container exit from the signal request. It also records pre-stop
  duration and total shutdown time from the start of the shared termination grace period. These
  are observations from the local runtime, not application performance service-level objectives.
- The top-level `profile` identifies the resolved lifecycle preset. The `shutdown` object records
  the resolved deadline and bounded pre-stop execution evidence without command output.
- The `telemetry` object retains the v1 trace fields and adds a nested `metrics` object recording
  whether metric verification was enabled, its configured minimum, post-work data-point count, and
  accepted export request count. It never contains raw telemetry or the temporary receiver
  credential. For schema v1, the existing top-level `telemetry.enabled` field continues to mean
  trace verification enabled; metric-only consumers use `telemetry.metrics.enabled`.
- The `streaming.sse` object records bounded SSE protocol evidence separately from ordinary request
  traffic: establishment, signal-boundary activity, event counts, terminal-event observation, clean
  EOF, close timing, and a classified error without request URLs or header values.
- The `streaming.websocket` object records the opening-handshake status, negotiated subprotocol,
  signal-boundary activity, bounded message count, terminal-message timing, close frame/code/reason,
  close timing, and classified errors without request URLs, header values, or message payloads.
- The `streaming.grpc` object records method establishment, signal-boundary activity, a bounded
  response count, final gRPC status, close timing, and classified errors without request or response
  payloads, descriptors, or metadata values.
- The `traffic.driver` field records whether evidence came from the built-in `http` driver or the
  built-in `grpc` driver or the user-provided `command` driver. Existing traffic totals and
  assertion meanings are shared by all three.

### Repeated-run report

`draincheck repeat` writes a separate aggregate JSON contract with `schema_version: 1`. Existing
aggregate field names, JSON types, and timing meanings are preserved within v0.x; new fields may be
added and consumers must ignore them. Each referenced run directory also contains the stable
single-run JSON report described above.

The aggregate timing distribution includes passing runs only, including pre-stop and total
shutdown timing. Failed and incomplete runs remain
represented in run counts, verdicts, failed assertion names, errors, and JUnit output.
Configured repeat p95 budgets add stable `budget_failures` and `budget_assertions` fields. A budget
failure changes only the aggregate verdict; it does not change per-run verdicts or `runs_failed`.
Budget JUnit cases use the stable names documented in the [repeat guide](repeat.md).

### Scenario-suite report

`draincheck suite` writes a separate aggregate JSON contract with `schema_version: 1`. Existing
aggregate field names and JSON types are preserved within v0.x; new fields may be added and
consumers must ignore them. The aggregate identifies the shared image, runtime, lifecycle profile, requested and
completed scenario counts, assertion failures, execution errors, and each completed scenario's
stable filename-derived name, configuration path, run ID, timings, and artifact directory.

Each referenced scenario directory contains the stable single-run JSON and JUnit reports plus a
diagnostic debug bundle. Suite JUnit uses one test case per requested scenario: lifecycle assertion
failures are `failure` elements, runtime or reporting failures are `error` elements, and scenarios
not attempted after an execution error are `skipped` elements. See the [suite guide](suite.md) for the complete
layout and execution rules.

### JUnit report

JUnit remains a stable CI interchange: one `draincheck` test suite and one test case per lifecycle
assertion. Existing assertion names retain their meanings. Additional assertions, elements, or
attributes may be added, so consumers should parse XML rather than compare complete files.

### Debug bundle

The debug ZIP is diagnostic evidence, not a stable automation API. Entry names or internal fields
may grow between minor releases. Its safety promises are stable: output remains bounded, configured
request-, SSE-, and WebSocket-header values, gRPC metadata values, inline HTTP/gRPC request bodies,
command environment values, and secret-like target environment variables remain redacted,
file-backed body contents are not embedded, and files are written atomically.

## CLI and exit codes

The documented commands, flags, and exit codes are stable within `v0.2.x`:

| Code | Contract |
|---:|---|
| `0` | Lifecycle assertions passed. |
| `1` | A valid lifecycle run completed with failed assertions. |
| `2` | Configuration or command usage is invalid. |
| `3` | Runtime, preflight, reporting, cleanup, or internal execution prevented a valid pass. |
| `130` | Draincheck was interrupted and attempted cleanup. |

The `generic` and `kubernetes` profiles are local presets. The Kubernetes profile models a
single-container exec pre-stop hook, signal ordering, and one shared grace-period budget. It does
not parse manifests, contact a cluster, or make compatibility claims about Kubernetes versions,
network routing, sidecars, or control-plane behavior.

A patch release will not remove a command or flag or change an exit-code meaning. A future minor
release must announce a deprecation before removal, retain the old form for at least that minor
line, and provide a replacement or migration instructions.

## Security and operational boundary

- Draincheck invokes runtime commands without a shell and does not elevate container privileges.
- Command probes are also executed directly without a shell, but they are trusted repository code
  and run with the CI runner's own permissions. Draincheck bounds their protocol output, stderr,
  arguments, environment, and execution time; it does not sandbox them.
- Readiness and pre-stop exec commands run inside the target container without a shell. Their output
  is bounded and excluded from reports. Command arguments are retained in debug configuration, so
  they must not contain credentials; reference the container's environment from image-owned code
  when a hook needs configuration.
- Telemetry verification opens an ephemeral host TCP listener so the test container can export through
  `host.draincheck.internal`. The listener requires a random 256-bit per-run token, accepts only
  OTLP trace and metric protobuf requests up to 16 MiB, retains aggregate observations rather than
  telemetry payloads, and closes before evidence collection and container cleanup.
- When trace verification is enabled, Draincheck overrides `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`,
  `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`, and `OTEL_EXPORTER_OTLP_TRACES_HEADERS` inside the test
  container. When metric verification is enabled, it similarly overrides the signal-specific metric
  endpoint, protocol, and headers and appends a unique run marker to `OTEL_RESOURCE_ATTRIBUTES`.
  Exporters for disabled signals are unchanged.
- The operator is responsible for runtime access; membership in a Docker-equivalent group can grant
  host-level control.
- Images are never pulled unless `--pull missing` or `--pull always` is selected.
- CI should use disposable credentials and non-production dependencies for the traffic scenario.
- `--keep-on-failure` intentionally retains a container and should normally be disabled in CI.
- Draincheck has no hosted component and sends no product telemetry. The optional local receiver
  accepts only application telemetry explicitly requested by the lifecycle contract.

## Updating this contract

The tested-version evidence may advance in a patch release without changing the behavioral
contract. Narrowing support, changing a stable format, or removing a CLI surface requires release
notes and the compatibility process above. New built-in adapters must be labeled explicitly and do
not weaken the stable HTTP lifecycle contract or the bounded command-probe contract.
