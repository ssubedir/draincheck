---
title: SSE shutdown verification
description: Keep a server-sent events connection open and verify its terminal event during shutdown.
---

Draincheck can keep one server-sent events connection open alongside ordinary request traffic and
verify its shutdown behavior in the same CI lifecycle run. The probe is optional and disabled by
default.

```yaml
streaming:
  sse:
    enabled: true
    path: /events
    headers:
      Authorization: Bearer ci-only-token
    initial_event: ready
    terminal_event: shutdown
    establish_timeout: 2s
    close_timeout: 5s
```

`initial_event` and `terminal_event` match the value of the SSE `event:` field. Draincheck does not
interpret the event's `data:` payload. Set `terminal_event` to an empty string only when clean EOF,
without a named final event, is the application's intentional contract.

## Lifecycle order

Draincheck performs these steps:

1. Wait for the configured HTTP or gRPC readiness check.
2. Open `streaming.sse.path` with `GET` and `Accept: text/event-stream`.
3. Require HTTP `200`–`299`, the `text/event-stream` media type, and `initial_event` before
   `establish_timeout`.
4. Start the normal configured request traffic.
5. At the termination-signal request boundary, record whether the same SSE connection is active.
6. Require `terminal_event` when configured, followed by a complete event delimiter and clean EOF.
7. Require that closure to occur after the signal request and within `close_timeout`.

The SSE close budget starts when Draincheck requests the signal, not when the runtime later confirms
delivery. It must not exceed `shutdown.deadline`. The stream does not count as ordinary traffic and
does not change request success or in-flight counts.

## Assertions and reports

An enabled probe adds three JUnit and JSON assertions:

- `stream.established`: the valid response and initial event arrived in time.
- `stream.active_at_signal`: the established connection was open at the signal request.
- `stream.closed_gracefully`: the terminal event requirement, clean EOF, ordering, and close budget
  all passed.

The JSON and debug timeline expose bounded evidence under `streaming.sse`, including status,
content type, event count, initial and terminal observations, signal-boundary activity, EOF and
close-timing booleans, and a classified error. The report never includes the request URL or header
values. All configured SSE header values are redacted from debug bundles; do not place credentials
in the path or query string because the resolved configuration contains the path.

## Application behavior

The stream handler should flush the initial event immediately, remain blocked while the service is
running, and react to the application's shared shutdown state. A typical order is:

```text
receive SIGTERM
  -> mark readiness unhealthy
  -> emit and flush the SSE terminal event
  -> return from the stream handler
  -> finish other in-flight handlers
  -> flush telemetry and close dependencies
  -> exit
```

Do not rely on client reconnect behavior for this check. Draincheck observes one connection and
does not reconnect or send `Last-Event-ID`.

## Protocol limits

- Plaintext HTTP through `streaming.sse.container_port`, falling back to
  `target.container_port` when omitted.
- One SSE stream per run; HTTP redirects follow Go's standard client behavior.
- A maximum of 64 KiB per SSE line and bounded error messages.
- Comments are ignored. Events must end with a blank line; EOF in the middle of an event is
  truncated rather than clean.
- WebSockets use the separate [WebSocket shutdown adapter](websocket.md). Unary and server-streaming
  gRPC use the [gRPC lifecycle adapter](grpc.md). Client-streaming, bidirectional RPCs, TLS, and
  application-specific `data:` payload assertions remain outside the SSE adapter.
