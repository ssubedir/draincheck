---
title: HTTP traffic contract
description: Configure concurrent HTTP work, request bodies, headers, ports, and expected statuses.
---

Draincheck sends the configured HTTP request concurrently so real work is active when termination
begins. The request can include a fixed body and an exact expected-status contract, which makes
POST, PUT, and application-specific test endpoints usable in CI.

```yaml
traffic:
  driver: http
  # container_port: 8081 # omitted inherits target.container_port
  request:
    method: POST
    path: /jobs?delay=2s
    headers:
      Content-Type: application/json
      Authorization: Bearer ci-only-token
    body_file: ./testdata/draincheck-job.json
    success_statuses: [202, 409]
  count: 5
  concurrency: 5
  shutdown_after: 500ms
  request_timeout: 10s
```

## Request bodies

Configure no more than one body source:

- `body` contains a short inline string.
- `body_file` names a file relative to the directory containing `draincheck.yaml`. Absolute paths
  are accepted but make CI configurations less portable.

Draincheck reads and bounds the body before it creates the container. The maximum size is 1 MiB.
Every worker receives an independent reader over the same immutable bytes, so concurrent requests
and HTTP redirects can replay the payload safely. Draincheck does not infer `Content-Type`, parse
JSON, expand environment variables, or template per-request identifiers; declare required headers
and use deterministic test data.

The optional post-signal phase reuses the same method, path, headers, body, and status rule.

`http` is the default `traffic.driver`, so existing configurations may omit it. When
`traffic.driver` is `command` or `grpc`, the HTTP request block is ignored. The
[command probe protocol](command-probes.md) or [gRPC lifecycle adapter](grpc.md) supplies the
traffic evidence instead. `traffic.container_port` selects the workload listener for every traffic
driver and defaults to `target.container_port` when omitted.

## Expected status codes

When `success_statuses` is omitted or empty, HTTP `200` through `399` is successful. A non-empty
list replaces that default with exact codes between `100` and `599`:

```yaml
success_statuses: [202]
```

With this configuration, HTTP `202` succeeds and HTTP `200` fails. The rule also defines
post-signal acceptance: a matching response is accepted, while an unmatched status or transport
failure is rejected.

Use expected statuses to express the endpoint's real test contract, not to hide server errors. A
failed status is recorded as `http_status` with the numeric code in traffic evidence.

## Security and artifacts

Request bodies should contain disposable test data, never production credentials or customer
records. Inline bodies are replaced with `[REDACTED]` in debug configuration. File-backed contents
are held only in memory for the run and are never embedded in JSON, JUnit, or debug artifacts; the
configured file path remains visible. Draincheck also scrubs an exact configured body if that whole
value appears in captured evidence, but it cannot reliably recognize fragments independently logged
by the application.

Header values remain redacted under the existing debug-bundle contract. Protect container logs and
debug bundles with normal CI artifact access controls.
