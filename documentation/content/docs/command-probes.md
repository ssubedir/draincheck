---
title: Command traffic probes
description: Define application-specific in-flight work with a bounded repository-owned command protocol.
---

The command traffic driver lets repository-owned code define what “active work” and “successful
completion” mean. It is useful when the built-in HTTP client cannot safely exercise the workload,
such as a queue consumer, gRPC method, database job, or application-specific protocol.

Draincheck still owns the container, readiness polling, concurrency, signal boundary, deadlines,
assertions, reports, and cleanup. The command owns only one unit of traffic.

## Configuration

```yaml
traffic:
  driver: command
  command:
    executable: ./ci/draincheck-probe
    args: [--queue, lifecycle-test]
    environment:
      PROBE_TOKEN: disposable-ci-token
    working_directory: .
  count: 4
  concurrency: 4
  shutdown_after: 250ms
  request_timeout: 10s
  post_signal:
    policy: disabled
    delay: 0s
    count: 1
```

Draincheck launches one command process per work item, up to `concurrency` at a time. It does not
invoke a shell. `args` are passed literally, so pipes, redirection, variable expansion, and shell
quoting do not apply.

An executable containing `/` or `\` is resolved relative to the directory containing
`draincheck.yaml`; an absolute path stays absolute. A bare executable name is resolved through the
runner's `PATH`. `working_directory` is also config-relative and defaults to the configuration
directory.

## Environment contract

Each process inherits the runner environment, then receives `traffic.command.environment`, then
these authoritative values from Draincheck:

| Variable | Meaning |
|---|---|
| `DRAINCHECK_PROTOCOL_VERSION` | Protocol version; currently `1`. |
| `DRAINCHECK_TARGET_URL` | Mapped target origin, such as `http://127.0.0.1:49152`. |
| `DRAINCHECK_RUN_ID` | Unique lifecycle run identifier. |
| `DRAINCHECK_REQUEST_ID` | One-based identifier for this probe process. |
| `DRAINCHECK_PHASE` | `initial` or `post_signal`. |

Configuration cannot set names with the reserved `DRAINCHECK_` prefix. Do not place secrets in
arguments because process listings and diagnostics may expose them. Command environment values are
redacted from the debug configuration and scrubbed from captured evidence when their exact value
appears.

## Stdout protocol

Stdout is UTF-8 newline-delimited JSON and contains exactly two event objects in this order:

```json
{"type":"active"}
{"type":"result","success":true,"message":"job acknowledged and completed"}
```

1. During the `initial` phase, emit `active` only after the target application has begun the
   external work that must remain in flight across termination. Starting the helper process or
   opening a client is not enough. During `post_signal`, emit it after the acceptance attempt has
   been dispatched and is awaiting its outcome; the attempt need not ultimately be accepted.
2. Keep that work running, wait for its outcome, then emit one `result`.
3. Exit with status zero after writing the result.

For the initial phase, `success: true` means the work completed correctly; `false` is a failed
traffic request. For `DRAINCHECK_PHASE=post_signal`, `true` means the application accepted the new
work and `false` means it rejected it. This lets the same adapter support `post_signal.policy`.

`message` is optional and limited to 300 bytes. Unknown fields, unknown event types, duplicate or
out-of-order events, extra JSON values, and a missing event are protocol failures. Stdout is capped
at 64 KiB with an 8 KiB per-line limit. Write human diagnostics to stderr; Draincheck drains and
bounds it separately. A non-zero exit, protocol failure, or timeout is invalid adapter evidence and
cannot satisfy a post-signal accept or reject policy.

## Minimal Go adapter shape

```go
package main

import (
	"encoding/json"
	"os"
)

func main() {
	target := os.Getenv("DRAINCHECK_TARGET_URL")
	requestID := os.Getenv("DRAINCHECK_REQUEST_ID")

	work := startApplicationWork(target, requestID)
	if err := work.WaitUntilActive(); err != nil {
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "active"})

	err := work.Wait()
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"type":    "result",
		"success": err == nil,
	})
}
```

The adapter must keep stdout protocol-only and should enforce its own operation-specific bounds in
addition to Draincheck's `request_timeout`.

## Telemetry and security boundary

Trace flush verification is not available with the command driver because Draincheck cannot inject
the request-specific W3C trace context into an arbitrary protocol. Metric flush verification may
still be enabled because it uses a run-level resource marker.

The command runs on the CI host with the same permissions as Draincheck. It is trusted code, not a
sandboxed plugin. Review it like any other pipeline script, pin third-party dependencies, use only
disposable test credentials and data, and never construct its executable or arguments from
untrusted pull-request input.
