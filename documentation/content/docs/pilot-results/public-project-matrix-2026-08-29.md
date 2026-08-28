---
title: "Public-project matrix: 2026-08-29"
description: Lifecycle evidence from recognizable public container projects.
---

## Scope

This record captures Draincheck runs against recognizable public container projects. It is product
evidence, not a compatibility certification or a claim that the upstream maintainers endorse
Draincheck.

- Date: 2026-08-29 (America/New_York)
- Draincheck base revision: `14592a2`
- Host: Windows with a rootless Podman machine
- Runtime client and Linux server: Podman 6.0.2
- Architecture: `linux/amd64`

The matrix uses these immutable upstream artifacts:

- [postmanlabs/httpbin](https://github.com/postmanlabs/httpbin), documented Docker image
  `docker.io/kennethreitz/httpbin@sha256:599fe5e5073102dbb0ee3dbb65f049dab44fa9fc251f6835c9990f8fb196a72b`.
  The published image was created in 2018, so this is a legacy lifecycle baseline rather than a
  recommendation for a current production dependency.
- [NGINX 1.31.4](https://nginx.org/2026.html), official Alpine OCI index
  `sha256:db35bfc6b2951e7f8a72db5db120288c127ffaeeb4a6d4b95a26fead017d5913`.
- [WireMock Docker 3.13.2-3](https://github.com/wiremock/wiremock-docker/releases/tag/3.13.2-3),
  official OCI index
  `sha256:0d4ecb3e4dc8213fd7a4d37d6a78f6e6b553a6d2e15bd51b0999781282ac61b3`.

NGINX receives only a configuration file and a deterministic two-MiB static file. WireMock receives
only two declarative mappings. No upstream application source is patched.

## Results

| Project | Run 1 | Run 2 | Run 3 | In flight | Request result | Process result |
|---|---:|---:|---:|---:|---|---|
| Postman HTTPBin | 8.168 s | 8.431 s | 8.070 s | 5 | 5/5 completed | Pass, exit `0` |
| NGINX 1.31.4 | 5.390 s | 5.130 s | 4.774 s | 5 | 5/5 completed | Pass, exit `0` |
| WireMock 3.13.2-3 | 5.542 s | 5.853 s | 5.321 s | 3 | 0/3 successful | Expected fail, exit `143` |

Every request counted above was still active when signal delivery was confirmed. Readiness stopped
succeeding in all nine runs. Draincheck removed every test container, and none of the runs required
forced cleanup.

## Findings

Postman HTTPBin's Gunicorn process completed all delayed requests after `SIGTERM` and exited `0`.
This shows that Draincheck can exercise a legacy Python service directly from a pinned public image,
without fixture-specific environment controls.

NGINX completed every rate-limited response after its documented graceful `SIGQUIT`, stopped
accepting readiness requests, and exited `0`. This demonstrates why the shutdown signal is part of
the lifecycle contract: testing `SIGTERM` would exercise NGINX's fast-shutdown path instead.

WireMock's official entrypoint ends with `exec`, so the JVM received `SIGTERM` directly rather than
through a non-forwarding shell. Across all three runs it exited `143` before the delayed responses
completed. Draincheck consistently reported:

- `traffic.failed_requests`
- `traffic.inflight_complete`
- `shutdown.exit_code`

The result does not assert that WireMock is defective for its intended testing use. It establishes
that this image does not satisfy the declared container-orchestrator contract of completing active
HTTP work and exiting `0` after `SIGTERM`.

## Product evidence and limits

- The same CLI contract distinguishes two passing upstream lifecycles from one expected failure.
- Pinned direct images and configuration-only derived images work without application source
  ownership.
- Exit status, failed assertions, traffic outcomes, and cleanup evidence are stable across repeated
  runs.
- The matrix can run as a normal CI job and retain machine-readable evidence even for an expected
  negative case.

This remains maintainer-run research. It does not measure unfamiliar-user setup time, whether an
upstream service team would adopt Draincheck, or whether the diagnostics lead that team to a fix.
