---
title: "External pilot: go-httpbin v2.25.0"
description: A reproducible external-image lifecycle baseline captured with Podman.
---

## Scope

This record captures a reproducible external-image baseline for Draincheck. It does not represent
service-owner adoption or a compatibility certification.

- Date: 2026-08-28 (America/New_York)
- Draincheck revision: `09faba3`
- Host: Windows with a rootless Podman machine
- Runtime client: Podman 6.0.2
- Service: [mccutchen/go-httpbin v2.25.0](https://github.com/mccutchen/go-httpbin/releases/tag/v2.25.0)
- OCI index: `ghcr.io/mccutchen/go-httpbin@sha256:20739736d4eb8dc1b998dff701f437b8bd62dcc46492bd0d861e89890ca36500`

The service is maintained outside the Draincheck repository. Its command package catches
`SIGTERM`/`SIGINT` and calls Go's `http.Server.Shutdown` with a bounded context. The committed
Draincheck scenario uses `/get` for readiness and starts five concurrent `/delay/2s` requests before
sending `SIGTERM`.

## Result

Three consecutive runs passed:

| Run | Signal confirmation | Readiness stopped succeeding | Container exit after signal request | Total | In-flight result |
|---:|---:|---:|---:|---:|---:|
| 1 | 386 ms | 408 ms | 1.624 s | 4.807 s | 5/5 successful |
| 2 | 363 ms | 408 ms | 1.540 s | 4.686 s | 5/5 successful |
| 3 | 345 ms | 407 ms | 1.567 s | 4.782 s | 5/5 successful |

Every run exited with status `0`, required no forced kill, and left no container with the
Draincheck run label. JSON, JUnit, and debug-bundle evidence was generated locally under
`reports/external-pilots/go-httpbin/` and remains intentionally untracked.

## Observation

The readiness endpoint did not return an explicit non-ready status. The HTTP listener stopped
accepting connections while existing handlers drained, so readiness stopped succeeding before the
container exited. Draincheck recorded the transport-level transition separately from an HTTP status
transition.

This passes the current declared contract: readiness must stop succeeding, in-flight requests must
finish, and the process must exit cleanly. It does not claim that a particular load balancer will
avoid sending new work during endpoint-propagation delay. Post-signal request policy remains an
explicit future assertion rather than an implicit platform compatibility claim.

## Product evidence

- Draincheck can pull and inspect an immutable third-party multi-platform image reference.
- The signal request and runtime-confirmed delivery remain distinct in the timeline.
- Requests active at confirmed signal delivery are identified and evaluated correctly.
- A real `http.Server.Shutdown` path drains without fixture-specific environment controls.
- Cleanup remains exact and leak-free across repeated runs.

This pilot does not measure first-time setup by an unfamiliar team, diagnostic usefulness after an
application failure, or willingness to make Draincheck a required release gate. Those require an
actual service-owner pilot.
