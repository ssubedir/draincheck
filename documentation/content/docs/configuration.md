---
title: Configuration reference
description: Understand every Draincheck YAML section, driver, default, and lifecycle assertion.
---

A Draincheck configuration describes one lifecycle test against one final container image. Run
`draincheck init` to write the complete commented template, then keep only the behavior the service
owns and can exercise safely in CI.

```bash
draincheck init --image checkout:local --port 8080
draincheck validate --config draincheck.yaml
```

Draincheck applies documented defaults before reading the file. Explicit YAML overrides those
defaults, unknown fields are rejected, and multiple YAML documents are not supported.

## Configuration map

| Section | Required | Purpose |
|---|---:|---|
| `version` | Yes | Selects the YAML contract version. The current and only value is `1`. |
| `target` | Yes | Identifies the image, default application port, and container environment. |
| `readiness` | Yes | Defines how startup readiness and post-signal withdrawal are observed. |
| `traffic` | Yes | Defines meaningful work that must be active when termination begins. |
| `streaming` | No | Holds SSE, WebSocket, or server-streaming gRPC work across termination. |
| `telemetry` | No | Verifies final correlated traces and metrics reach Draincheck's temporary receiver. |
| `repeat` | No | Adds aggregate p95 budgets for repeated runs. |
| `shutdown` | Yes | Selects the signal, total deadline, and optional pre-stop command. |
| `assertions` | Yes | Defines the lifecycle outcomes that determine the process exit code. |

## General rules

- Durations use Go-style values such as `200ms`, `2s`, and `1m30s`.
- Ports are container ports from `1` through `65535`. Probe-specific ports inherit
  `target.container_port` when omitted.
- Relative file and executable paths resolve from the directory containing `draincheck.yaml`.
- Environment values and request bodies are literal. Draincheck does not expand shell variables or
  template values.
- `draincheck validate` catches unknown fields, incompatible driver blocks, invalid bounds, and
  cross-field deadline errors without starting a container.

The generated [JSON Schema](https://github.com/ssubedir/draincheck/blob/main/schema/draincheck.schema.json)
is the machine-readable field contract. The tables below document runtime meaning and defaults.

## `target`

| Field | Default | Meaning |
|---|---|---|
| `image` | Empty | Image to run. It is required unless an image is supplied to `verify`, `repeat`, or `suite`. |
| `container_port` | `8080` | Default listener used by readiness, traffic, and streaming probes. |
| `environment` | `{}` | Non-secret environment variables passed to the target container. |

```yaml
target:
  image: checkout:local
  container_port: 8080
  environment:
    APP_ENV: draincheck
```

## `readiness`

The same readiness check proves startup and observes withdrawal after the shutdown signal.

| Field | Default | Meaning |
|---|---|---|
| `driver` | `http` | One of `http`, `grpc`, or `exec`. |
| `container_port` | Target port | Optional listener override for HTTP or gRPC. Invalid with `exec`. |
| `path` | `/ready` | HTTP path. It must begin with `/`. |
| `success_status` | `200` | Exact HTTP status considered ready. |
| `grpc.service` | Empty | gRPC Health service name; empty requests overall server health. |
| `exec.command` | None | Argument vector run inside the container; exit `0` means ready. |
| `startup_timeout` | `20s` | Maximum time to establish readiness. |
| `interval` | `200ms` | Delay between checks; it cannot exceed `startup_timeout`. |

See [readiness verification](/docs/readiness) for driver examples and withdrawal semantics.

## `traffic`

Traffic must be genuine work that remains active long enough for Draincheck to request termination.

### Common traffic fields

| Field | Default | Meaning |
|---|---|---|
| `driver` | `http` | One of `http`, `grpc`, or `command`. |
| `container_port` | Target port | Optional workload-listener override. |
| `count` | `5` | Total work items. Must be at least `1`. |
| `concurrency` | `5` | Simultaneous work items, from `1` through `count`. |
| `shutdown_after` | `500ms` | Delay after traffic starts before requesting shutdown; may be `0s`. |
| `request_timeout` | `10s` | Per-work-item timeout. |
| `post_signal.policy` | `disabled` | `disabled`, `accept`, or `reject` for new work after signal delivery. |
| `post_signal.delay` | `0s` | Delay before post-signal probes; must be shorter than the shutdown deadline. |
| `post_signal.count` | `1` | Number of post-signal probes, from `1` through `100`. |

### HTTP request

| Field | Default | Meaning |
|---|---|---|
| `request.method` | `GET` | HTTP method. |
| `request.path` | `/work?delay=2s` | Absolute request path and optional query string. |
| `request.headers` | `{}` | Literal request headers; values are redacted from debug configuration. |
| `request.body` | Empty | Inline body, limited to 1 MiB. |
| `request.body_file` | Empty | Config-relative body file, limited to 1 MiB; mutually exclusive with `body`. |
| `request.success_statuses` | `200`–`399` | Optional exact success-code list replacing the default range. |

See the [HTTP traffic contract](/docs/http-traffic) for bodies, status matching, and artifact safety.

### gRPC request

| Field | Default | Meaning |
|---|---|---|
| `grpc.method` | None | Unary RPC in `package.Service/Method` form. |
| `grpc.request` | `{}` | Inline protobuf JSON request, limited to 1 MiB. |
| `grpc.request_file` | Empty | Config-relative protobuf JSON file; mutually exclusive with `request`. |
| `grpc.metadata` | `{}` | Literal outgoing metadata. |
| `grpc.descriptor_set` | Reflection | Optional config-relative descriptor set generated with imports. |
| `grpc.expected_codes` | `[OK]` | Accepted final gRPC status codes. |

See [gRPC lifecycle verification](/docs/grpc) for reflection, descriptors, correlation, and status
handling.

### Command request

| Field | Default | Meaning |
|---|---|---|
| `command.executable` | None | Trusted host executable or config-relative path. |
| `command.args` | `[]` | Argument vector, with no shell added. |
| `command.environment` | `{}` | Additional non-secret host-process environment. `DRAINCHECK_` is reserved. |
| `command.working_directory` | Config directory | Working directory, resolved relative to the configuration. |

See [command traffic probes](/docs/command-probes) for the active/result protocol and trust boundary.

## `streaming`

Each streaming adapter is independent and disabled by default. It runs alongside ordinary traffic.

| Block | Key defaults | What it proves |
|---|---|---|
| `sse` | `/events`, initial `ready`, terminal `shutdown`, `2s` establish, `5s` close | One SSE connection is active at signal and reaches its expected terminal boundary. |
| `websocket` | `/ws`, terminal message `shutdown`, close code `1000`, `2s` establish, `5s` close | One WebSocket remains active and closes with the declared application contract. |
| `grpc` | Minimum `1` message, final `OK`, `2s` establish, `5s` close | One server stream remains active and ends with the expected status. |

All three blocks support `enabled` and an optional `container_port`. SSE and WebSocket support
`path` and `headers`; WebSocket also supports `subprotocols`. gRPC streaming supports the same
request, metadata, reflection, and descriptor inputs as unary gRPC traffic. Establish timeouts must
not exceed `30s`, and enabled close timeouts cannot exceed `shutdown.deadline`.

See the [SSE guide](/docs/streaming), [WebSocket guide](/docs/websocket), and
[gRPC guide](/docs/grpc) for complete examples.

## `telemetry`

| Field | Default | Meaning |
|---|---|---|
| `traces.enabled` | `false` | Require correlated in-flight spans to reach the temporary OTLP/HTTP receiver. |
| `traces.minimum_correlated_spans` | `1` | Required correlated span count, from `1` through `100`. |
| `traces.flush_timeout` | `2s` | Positive wait budget, no longer than `30s`. |
| `metrics.enabled` | `false` | Require run-correlated metric points after in-flight work completes. |
| `metrics.minimum_data_points` | `1` | Required data-point count, from `1` through `10,000`. |
| `metrics.flush_timeout` | `2s` | Positive wait budget, no longer than `30s`. |

Trace verification works with HTTP or unary gRPC traffic, not command traffic. See
[OpenTelemetry shutdown-flush verification](/docs/telemetry) for exporter requirements and the exact
correlation boundary.

## `repeat`

The optional `repeat.budgets` block turns aggregate p95 timing into assertions for
`draincheck repeat`:

```yaml
repeat:
  budgets:
    startup_ready_p95: 2s
    readiness_withdrawal_p95: 750ms
    container_exit_p95: 5s
```

Omitted budgets remain informational. Configured values must be at least `1ms`. See
[repeated lifecycle verification](/docs/repeat) for aggregation and failure behavior.

## `shutdown`

| Field | Default | Meaning |
|---|---|---|
| `signal` | `SIGTERM` | Signal requested through the selected container runtime. |
| `deadline` | `15s` | Total shutdown budget. The Kubernetes profile defaults this to `30s`. |
| `pre_stop.exec.command` | None | Optional argument vector run inside the container before the signal. |

The pre-stop duration counts against the same deadline. See [lifecycle profiles](/docs/profiles) for
the Kubernetes ordering model and its boundary.

## `assertions`

| Field | Default | Meaning |
|---|---|---|
| `readiness_withdrawn_within` | `2s` | Maximum time from signal request until readiness stops succeeding. |
| `inflight_requests_complete` | `true` | Require work active at the signal boundary to finish successfully. |
| `max_failed_requests` | `0` | Maximum failed normal traffic items. |
| `exit_code` | `0` | Expected container exit code. |
| `forbid_force_kill` | `true` | Fail if Draincheck must force-remove the target. |

`readiness_withdrawn_within` cannot exceed `shutdown.deadline`. Assertion failures produce exit
code `1`; invalid configuration produces exit code `2`.

## Extended example

This example exposes the optional capability blocks without enabling all of them. Remove unused
blocks to keep a service contract reviewable.

```yaml
version: 1

target:
  image: checkout:local
  container_port: 8080
  environment:
    APP_ENV: draincheck

readiness:
  driver: http
  path: /ready
  success_status: 200
  startup_timeout: 20s
  interval: 200ms

traffic:
  driver: http
  request:
    method: POST
    path: /jobs?delay=2s
    headers:
      Content-Type: application/json
    body_file: ./testdata/draincheck-job.json
    success_statuses: [202]
  count: 5
  concurrency: 5
  shutdown_after: 500ms
  request_timeout: 10s
  post_signal:
    policy: reject
    delay: 100ms
    count: 1

streaming:
  websocket:
    enabled: true
    path: /ws
    terminal_message: shutdown
    close_code: 1000
    establish_timeout: 2s
    close_timeout: 5s

telemetry:
  traces:
    enabled: true
    minimum_correlated_spans: 1
    flush_timeout: 2s
  metrics:
    enabled: true
    minimum_data_points: 1
    flush_timeout: 2s

shutdown:
  signal: SIGTERM
  deadline: 15s

assertions:
  readiness_withdrawn_within: 2s
  inflight_requests_complete: true
  max_failed_requests: 0
  exit_code: 0
  forbid_force_kill: true
```
