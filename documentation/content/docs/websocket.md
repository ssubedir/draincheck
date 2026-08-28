---
title: WebSocket shutdown verification
description: Verify terminal messages and close handshakes for a connection held across shutdown.
---

Draincheck can hold one receive-only WebSocket connection open while ordinary traffic exercises the
service. It proves that the connection survives until termination begins and then observes the
application's terminal message and close handshake.

## Configuration

```yaml
streaming:
  websocket:
    enabled: true
    # container_port: 8081 # omitted inherits target.container_port
    path: /ws
    headers:
      Authorization: Bearer disposable-ci-token
    subprotocols: [draincheck.v1]
    terminal_message: shutdown
    close_code: 1000
    establish_timeout: 2s
    close_timeout: 5s
```

`path` is joined to the random loopback HTTP mapping selected by
`streaming.websocket.container_port` and upgraded to a WebSocket connection. The port defaults to
`target.container_port`. The built-in observer supports plaintext `ws`; TLS and custom certificate
authorities remain outside the built-in support boundary.

`subprotocols` contains up to 16 WebSocket protocol tokens offered in order. The negotiated value is
recorded in the report, but an empty negotiated value is valid; use an endpoint that rejects the
handshake when a subprotocol is mandatory. Custom handshake headers are supported and their values
are redacted from debug artifacts.

## Lifecycle contract

The opening handshake must complete within `establish_timeout`. A successful HTTP `101` handshake
is enough to establish the observation; Draincheck does not require an initial application message.
It starts ordinary traffic only after every enabled streaming observation is established.

```text
WebSocket handshake
  -> ordinary traffic becomes active
  -> signal requested; socket must still be active
  -> optional terminal message
  -> peer close frame with expected code
```

`terminal_message` is matched byte-for-byte against complete text or binary message payloads. The
message must arrive after Draincheck requests the termination signal. Set it to an empty string when
the application has no terminal application message and the close frame alone is the contract.

`close_code` is the exact peer close code required for a pass and defaults to normal closure `1000`.
Sendable standard codes and application codes through `4999` are accepted in configuration;
reserved non-wire codes such as `1005`, `1006`, and `1015` are rejected.

`close_timeout` begins at the signal request and must not exceed `shutdown.deadline`. A passing
connection must have been active at that boundary, close afterward and within the timeout, include a
close frame with the expected code, and satisfy the optional terminal-message requirement. A TCP EOF
or reset without a close frame is not a clean WebSocket shutdown.

## Limits and evidence

Draincheck receives application messages but never stores their payloads. Each message is limited to
64 KiB and each connection to 10,000 messages. Close reasons and negotiated subprotocol evidence are
bounded to 256 bytes. Compression is disabled. Ping frames are handled by the client library while
the observation is reading.

The JSON and debug timeline expose evidence under `streaming.websocket`, including:

- handshake status and negotiated subprotocol;
- activity at signal request;
- total messages and whether the terminal message arrived after signal;
- close-frame presence, code, bounded reason, and timing;
- classified handshake, transport, size-limit, or cancellation errors.

JUnit receives `websocket.established`, `websocket.active_at_signal`, and
`websocket.closed_gracefully` assertion cases when the feature is enabled.

## Receive-only boundary

The built-in observer does not send a subscription, authentication, or application payload after
the opening handshake. This keeps the generic lifecycle contract deterministic and prevents
Draincheck from inventing protocol semantics.

If the endpoint requires a client message before it becomes active, use the
[command traffic adapter](command-probes.md) with your normal WebSocket client. That adapter can
perform the application handshake and report its own active/result boundary while Draincheck still
owns container lifecycle orchestration.
