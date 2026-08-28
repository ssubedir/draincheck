---
title: Post-signal request policy
description: Verify whether an application intentionally accepts or rejects new work after termination begins.
---

Draincheck can optionally verify what happens to new work after the container runtime confirms
delivery of the termination signal. This assertion is disabled by default because accepting work
during endpoint propagation and rejecting it immediately are both valid application contracts.

## Configuration

With the HTTP driver, the phase reuses the method, path, headers, body, and success-status rule under
`traffic.request`:

```yaml
traffic:
  request:
    method: GET
    path: /work?delay=2s
    headers: {}
  count: 5
  concurrency: 5
  shutdown_after: 500ms
  request_timeout: 10s
  post_signal:
    policy: reject
    delay: 250ms
    count: 2
```

`policy` has three values:

- `disabled` sends no post-signal traffic and adds no policy assertion.
- `accept` requires every post-signal response to match `traffic.request.success_statuses`. When
  that list is omitted, the default is HTTP `200` through `399`.
- `reject` requires every post-signal request to return another HTTP status or fail at the transport
  layer.

With the command driver, Draincheck runs a new command process for every post-signal probe and sets
`DRAINCHECK_PHASE=post_signal`. A `result` event with `success: true` means accepted and
`success: false` means rejected. A malformed protocol, non-zero command exit, or timeout is invalid
evidence and cannot satisfy either policy. See the [command probe protocol](command-probes.md).

With the gRPC driver, the phase repeats `traffic.grpc`. A status in `expected_codes` means accepted;
another server status means rejected. Descriptor, request-decoding, timeout, cancellation, and
transport failures are invalid evidence and cannot satisfy either policy. See the
[gRPC lifecycle guide](grpc.md).

`delay` starts at confirmed signal delivery, not at the earlier signal-request timestamp. It must
be shorter than `shutdown.deadline`. `count` is limited to 100, and all configured probes begin
concurrently. Signal-delivery latency still consumes the shutdown budget; if the phase cannot begin
before that budget expires, it remains incomplete and fails the active policy. Incomplete requests
fail both active policies.

## Timeline and reports

The normal traffic phase starts before termination and proves in-flight work can drain. The
post-signal phase starts afterward and tests only new work:

```text
initial traffic active
  -> signal requested
  -> signal delivery confirmed
  -> post_signal.delay
  -> post-signal requests start
  -> container drains and exits
```

The JSON and debug timeline report configured, started, completed, accepted, and rejected
post-signal counts under `traffic.post_signal`. A mismatch fails
`traffic.post_signal_policy`, which also becomes a JUnit test case. Rejections in this phase do not
increase the original traffic `failed` count or affect `traffic.failed_requests`.

Choose a deterministic, safe request. The configured delay should model an intentional application
or routing window rather than compensate for race-prone signal handling. Run the contract against a
fresh container with `draincheck repeat` if the transition is timing-sensitive.
