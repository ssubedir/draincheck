---
title: Readiness verification
description: Observe startup and withdrawal with HTTP, gRPC Health, or an in-container command.
---

Draincheck can observe readiness through an HTTP endpoint, the standard gRPC Health Checking
Protocol, or a command executed inside the target container. The selected check controls both
startup and readiness withdrawal; it does not change the traffic driver.

## HTTP readiness

HTTP remains the default and preserves existing configurations:

```yaml
readiness:
  driver: http
  container_port: 8080 # optional; defaults to target.container_port
  path: /ready
  success_status: 200
  startup_timeout: 20s
  interval: 200ms
```

Draincheck sends `GET` requests until `success_status` is observed. After the shutdown signal, any
other status or a connection that stops succeeding counts as withdrawal.

## gRPC health readiness

Select `grpc` for an application that implements `grpc.health.v1.Health`:

```yaml
readiness:
  driver: grpc
  container_port: 50051 # optional; defaults to target.container_port
  grpc:
    service: example.jobs.v1.Worker # empty checks overall server health
  startup_timeout: 20s
  interval: 200ms
```

Draincheck invokes the standard unary `Check` RPC. The configured service name is sent unchanged;
an empty value requests overall server health. Server reflection, protobuf descriptors, request
JSON, and metadata are not required.

`SERVING` is the only ready response. `NOT_SERVING`, `UNKNOWN`, `SERVICE_UNKNOWN`, an RPC status,
or a transport failure remains unready during startup. Draincheck retries until
`startup_timeout`, while also detecting a container that exits early.

After the signal request, the first non-`SERVING` response or failed RPC counts as readiness
withdrawal. Prefer returning `NOT_SERVING` before stopping the gRPC server: this produces stronger
evidence than a connection failure and preserves the listener for in-flight RPCs.

## Container exec readiness

Select `exec` when the image already contains a fast health command or when readiness is not
available through a mapped listener:

```yaml
readiness:
  driver: exec
  exec:
    command: ["/app/healthcheck", "--ready"]
  startup_timeout: 20s
  interval: 200ms
```

Draincheck invokes the argument vector directly through `docker exec` or `podman exec`; it does not
use a shell. Exit `0` means ready and any other exit code means not ready. Exit `126` and `127` are
reported as command startup or missing-command failures. The command runs as the container's
configured user with its container environment, working directory, filesystem, and network.

The command must be side-effect free and normally finish well below the one-second per-check
deadline. A timeout is a terminal startup-readiness failure because terminating the Docker or
Podman client cannot guarantee that an already-started process inside the container was killed.
Draincheck caps captured stdout and stderr at 4 KiB each and does not include either stream in its
report.

Exec readiness publishes no readiness port. Traffic and enabled streaming probes still publish
their own configured ports. The same command is run after the shutdown signal, so it must return a
non-zero exit promptly when the application begins draining.

## Shared lifecycle contract

For every driver:

- Each individual check is bounded to at most one second.
- `interval` controls the delay between checks.
- `assertions.readiness_withdrawn_within` starts at the signal request, not signal confirmation.
- Reports retain the stable `startup.ready` and `readiness.withdrawn` assertion names and identify
  the observed HTTP status, gRPC health state, or exec exit code in their messages.
- For HTTP and gRPC, `readiness.container_port` may differ from traffic and streaming ports;
  omission inherits `target.container_port`. It is invalid for exec readiness.

HTTP and gRPC readiness are plaintext in the current support boundary. TLS, custom certificate
authorities, mutual TLS, gRPC Health `Watch`, and custom readiness RPCs are not supported.
