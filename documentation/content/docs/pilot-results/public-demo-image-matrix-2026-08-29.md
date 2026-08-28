---
title: "Public demo-image matrix: 2026-08-29"
description: Lifecycle evidence from unmodified public HTTP demonstration and debugging images.
---

## Scope

This record captures Draincheck runs against unmodified public images commonly used to demonstrate
or debug HTTP infrastructure. It is product evidence, not compatibility certification or upstream
endorsement.

- Date: 2026-08-29 (America/New_York)
- Draincheck base revision: `21d12fa`
- Host: Windows with a rootless Podman machine
- Runtime client and Linux server: Podman 6.0.2
- Architecture: `linux/amd64`

The matrix uses immutable upstream multi-platform image digests:

- [go-httpbin v2.25.0](https://github.com/mccutchen/go-httpbin/releases/tag/v2.25.0):
  `ghcr.io/mccutchen/go-httpbin@sha256:20739736d4eb8dc1b998dff701f437b8bd62dcc46492bd0d861e89890ca36500`.
- [Postman HTTPBin](https://github.com/postmanlabs/httpbin):
  `docker.io/kennethreitz/httpbin@sha256:599fe5e5073102dbb0ee3dbb65f049dab44fa9fc251f6835c9990f8fb196a72b`.
- [Mendhak HTTP/HTTPS Echo 41](https://github.com/mendhak/docker-http-https-echo):
  `ghcr.io/mendhak/http-https-echo@sha256:2046be25f4a2c0bdda662ebfb7c2b7b60fc95c31d97987be143645a8a2194a40`.
- [Traefik Whoami v1.12.0](https://github.com/traefik/whoami/releases/tag/v1.12.0):
  `docker.io/traefik/whoami@sha256:c4717a8d1f0134a7444e24f881160e033991f23027c6c5a9a3f8fd22e70d1d44`.

Draincheck runs each published image directly. There are no pilot Dockerfiles, copied mappings,
entrypoint replacements, or patched application sources.

## Results

| Project | Runs | Duration | Active at confirmed signal | Request result | Process result |
|---|---:|---|---:|---|---|
| go-httpbin | 1 | 5.238 s | 5 | 5/5 completed | Pass, exit `0` |
| Postman HTTPBin | 1 | 8.274 s | 5 | 5/5 completed | Pass, exit `0` |
| Mendhak HTTP/HTTPS Echo | 3 | 10.944–11.152 s | 5 each run | 5/5 completed each run | Pass, exit `0` |
| Traefik Whoami | 3 | 3.800–3.841 s | 0 each run | 0/5 successful each run | Expected fail, exit `2` |

Every run began with five requests active before Draincheck requested `SIGTERM`. All containers were
removed and none required forced cleanup.

## Findings

go-httpbin and Postman HTTPBin both use an ordinary application route for startup readiness. Their
listeners stopped accepting readiness checks after `SIGTERM`, while all five delayed requests
remained active at confirmed signal delivery and completed before a clean exit.

Mendhak HTTP/HTTPS Echo has no dedicated readiness route. The pilot uses its ordinary catch-all
handler as the startup barrier, then sends delayed JSON POST requests through its Express body,
cookie, logging, header, and response-delay middleware. Across three fresh containers, all work
remained active at confirmed signal delivery, completed successfully, and exited `0`. Readiness
withdrawal was observed as listener closure rather than an explicit non-ready response; Draincheck's
timeline preserves that weaker evidence.

Traefik Whoami provides a real `/health` route and a built-in `wait` query parameter, but v1.12.0
does not gracefully shut down its HTTP server. In all three runs, five requests were active before
the signal request but had already failed by the time runtime delivery was confirmed. Draincheck
consistently reported:

- `traffic.inflight_exercised`
- `traffic.failed_requests`
- `shutdown.exit_code`

This is the useful contrast: possessing a health endpoint does not prove that an image drains work,
while lacking a dedicated readiness endpoint does not prevent lifecycle testing when a stable
ordinary route can serve as the startup barrier.

## Evidence limits

- The images are demonstration and debugging services, not production application pilots.
- Readiness observed only through listener closure is weaker than explicit readiness withdrawal.
- These runs bypass ingress controllers, gateways, service-mesh sidecars, and other infrastructure
  not packaged in the target image.
- Maintainer-run results do not measure unfamiliar-user setup time or diagnostic usefulness for a
  real service owner.
