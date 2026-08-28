---
title: Runtime pilot validation
description: Run Draincheck's controlled and public-image validation matrices across Docker and Podman.
---

Draincheck's runtime pilot suite exercises the same lifecycle contract against small HTTP services
implemented in several application runtimes. It is project validation for Draincheck itself, not a
language compatibility checker and not a claim that every framework or application using a tested
runtime will shut down correctly.

## Validation layers

| Layer | Purpose | Runs against |
|---|---|---|
| Unit tests | Prove command construction, state transitions, assertions, and reports | Fakes and deterministic inputs |
| Lifecycle conformance | Prove good and intentionally broken behavior is classified correctly | The controlled Go fixture |
| Runtime pilots | Detect assumptions that only work for the Go fixture | Graceful Node.js, Python, Java, and .NET images |
| Service-owner pilots | Validate setup time and diagnostic value on real release images | Images supplied by participating teams |

The committed runtime pilots use only each platform's built-in HTTP facilities. This keeps the
contract focused on process signals, readiness, active requests, and exit behavior rather than on
package-manager or framework dependencies.

## Current matrix

| Case | Runtime stream | Container base | Expected exit |
|---|---|---|---:|
| `node` | Node.js 24 LTS | `node:24-alpine` | `0` |
| `python` | Python 3.14 | `python:3.14-alpine` | `0` |
| `java` | Java 21 LTS | `eclipse-temurin:21-*-alpine` | `143` |
| `dotnet` | .NET 10 LTS | `mcr.microsoft.com/dotnet/{sdk,aspnet}:10.0` | `0` |

Major-version tags are intentional. A pilot should continuously exercise supported runtime streams
and expose lifecycle regressions introduced by a refreshed base image. Release artifacts use a
separate deterministic and attestable build path.

Each service implements this sequence:

1. Listen on port `8080` and return `200` from `/ready`.
2. Keep `/work?delay_ms=2000` active for two seconds.
3. On `SIGTERM`, immediately withdraw readiness.
4. Stop accepting work only after the withdrawal is observable.
5. Let active requests finish and exit with the service's declared status before the deadline.

Node.js, Python, and .NET declare exit status `0`. The Java pilot declares `143`: HotSpot runs its
shutdown hook and drains successfully on `SIGTERM`, while preserving the conventional
signal-derived status. Keeping that policy explicit exercises Draincheck's configurable exit-code
assertion and avoids hiding a common container behavior behind a wrapper process.

## Run locally

Run every case with Docker:

```bash
make pilot
```

Run one case, or use Podman:

```bash
make pilot PILOT_CASE=node
make pilot RUNTIME=podman PILOT_CASE=python
```

Without GNU Make:

```bash
DRAINCHECK_PILOT_RUNTIME=docker \
DRAINCHECK_PILOT_CASE=node,python \
DRAINCHECK_PILOT_REPORT_DIR=reports/pilot-docker \
go test -v -count=1 ./testdata/pilot
```

On Windows PowerShell, an explicit client path can be supplied when Podman is not on `PATH`:

```powershell
$env:DRAINCHECK_PILOT_RUNTIME = "podman"
$env:DRAINCHECK_PILOT_RUNTIME_BINARY = "$env:LOCALAPPDATA\Programs\Podman\podman.exe"
$env:DRAINCHECK_PILOT_CASE = "node"
$env:DRAINCHECK_PILOT_REPORT_DIR = "reports\pilot-podman"
go test -v -count=1 ./testdata/pilot
```

Valid case filters are `node`, `python`, `java`, `dotnet`, a comma-separated subset, or `all`.

## Evidence and failure behavior

Every case writes a JSON report, JUnit report, and redacted debug bundle. `summary.json` records the
runtime client version, selected language and base images, built image ID, Draincheck run ID,
duration, and verdict. Reports default to `reports/pilot-<runtime>/`, which is ignored by Git.

The harness fails unless all requests start and complete, at least one request is active when signal
delivery is confirmed, every assertion passes, and the lifecycle includes ready, terminating,
exited, and cleanup evidence. It checks for containers using both Draincheck's run label and the
unique pilot image. A leaked container is removed by exact ID and still fails the pilot.

The `Runtime pilots` GitHub Actions workflow runs the four cases as isolated Docker jobs when the
pilot workflow or fixtures change, on manual dispatch, and weekly. Ordinary product changes use the
core conformance jobs instead of starting the pilot matrix. Evidence uploads happen even when a
pilot fails.

## External-image baseline: go-httpbin

The first external-image pilot uses
[go-httpbin v2.25.0](https://github.com/mccutchen/go-httpbin/releases/tag/v2.25.0), an independently
maintained HTTP service. Its published image is referenced by the immutable multi-platform digest
`sha256:20739736d4eb8dc1b998dff701f437b8bd62dcc46492bd0d861e89890ca36500` rather than by a mutable
tag. The scenario uses `/get` for readiness and five concurrent `/delay/2s` requests for active work.

Run it with:

```bash
make external-pilot
make external-pilot RUNTIME=podman
```

Without Make, build Draincheck and execute the committed configuration with `--pull missing`:

```bash
go build -trimpath -o bin/draincheck ./cmd/draincheck
bin/draincheck verify \
  --runtime docker \
  --pull missing \
  --config testdata/pilot/external/go-httpbin/draincheck.yaml \
  --report-json reports/external-pilots/go-httpbin/draincheck.json \
  --report-junit reports/external-pilots/go-httpbin/draincheck.xml \
  --debug-bundle reports/external-pilots/go-httpbin/draincheck-debug.zip \
  --no-color
```

Three consecutive Podman 6.0.2 runs passed: all five requests were active when `SIGTERM` delivery
was confirmed, all five completed successfully, readiness stopped succeeding within the configured
budget, the container exited `0`, and no labeled resources remained. The independent source uses
Go's `http.Server.Shutdown` for its termination path, so this result exercises application code that
Draincheck does not own. The timings and observations are preserved in the
[pilot result](pilot-results/go-httpbin-v2.25.0.md).

This baseline is still not service-owner validation: go-httpbin is a testing service, no owner was
observed adopting Draincheck, and the passing path does not measure diagnostic usefulness after a
real regression. It reduces fixture bias while keeping the next decision gate honest.

## Public demo-image lifecycle matrix

The external-image job in the `Runtime pilots` workflow exercises four recognizable public demo
services. Every image is run directly from an immutable upstream digest. Draincheck does not build
a derived image, replace its entrypoint, or inject application configuration.

| Case | Upstream | Scenario | Expected result |
|---|---|---|---|
| `go-httpbin` | [mccutchen/go-httpbin](https://github.com/mccutchen/go-httpbin) | Ordinary `/get` readiness; five concurrent `/delay/2s` requests | Pass |
| `postman-httpbin` | [postmanlabs/httpbin](https://github.com/postmanlabs/httpbin) | Five concurrent `/delay/2` requests; `SIGTERM` | Pass |
| `mendhak-http-https-echo` | [mendhak/http-https-echo](https://github.com/mendhak/docker-http-https-echo) | Ordinary catch-all readiness; five delayed JSON POSTs through Express middleware | Pass |
| `traefik-whoami` | [Traefik Whoami](https://github.com/traefik/whoami) | Built-in `/health`; five `/?wait=2s` requests; `SIGTERM` | Expected lifecycle failure |

The Mendhak case directly addresses services without a dedicated readiness route. Its catch-all
application handler returns the startup `200`; after `SIGTERM`, listener closure supplies weaker
readiness-withdrawal evidence while its Express shutdown handler lets active POST requests finish.
The request includes JSON body parsing and upstream-defined delay/header middleware rather than a
Draincheck-specific handler.

The Whoami case is intentionally *expected negative*. The unmodified v1.12.0 image has a real
`/health` route and a built-in delayed demo request, but its server does not perform graceful HTTP
shutdown. The workflow requires Draincheck exit `1` and the `traffic.inflight_exercised`,
`traffic.failed_requests`, and `shutdown.exit_code` assertions. A different exit or missing
assertion fails the workflow. This demonstrates that health-check availability does not prove
draining behavior.

The matrix runs on relevant pull requests and pushes to `main`, by manual dispatch, and weekly.
Each case uploads the runtime version, console output, JSON/JUnit reports, and debug bundle.
Local Podman results for the direct-image matrix are recorded in the
[public demo-image pilot result](pilot-results/public-demo-image-matrix-2026-08-29.md). The earlier
configuration-derived NGINX/WireMock research remains preserved as a historical
[public-project result](pilot-results/public-project-matrix-2026-08-29.md); those derived images are
no longer part of the active matrix.

Run a passing direct-image case with the existing Make target by overriding its configuration and
report directory:

```bash
make external-pilot \
  RUNTIME=podman \
  EXTERNAL_PILOT_CONFIG=testdata/pilot/external/postman-httpbin/draincheck.yaml \
  EXTERNAL_PILOT_REPORT_DIR=reports/external-pilots/postman-httpbin
```

All active public cases use the same command shape and require no local image build. Override
`EXTERNAL_PILOT_CONFIG` and `EXTERNAL_PILOT_REPORT_DIR` for another case. Traefik Whoami returning
`1` with the three required assertions above is the recorded expected outcome, not a successful
lifecycle contract.

## Add or update a case

Keep pilot services deliberately small:

- Use an official, supported runtime image and an exec-form entrypoint.
- Use built-in HTTP and shutdown facilities when practical.
- Implement `/ready` and `/work?delay_ms=2000` on port `8080`.
- Make readiness false as soon as termination starts.
- Preserve active work and exit without needing a forced kill.
- Add the case to `pilotCases` in `testdata/pilot/pilot_test.go` and to the workflow matrix.
- Run the case with both Docker and Podman before merging when both are available.

Do not add intentionally broken behavior to the synthetic runtime services; that belongs in the
lifecycle conformance suite. An external project may be recorded as expected negative only when
its unmodified lifecycle behavior is reproducible and the required diagnostic assertions are
explicitly checked.
Do not convert a passing runtime pilot into a broad framework or platform compatibility claim.

## Real service-owner pilot record

The synthetic matrix makes real pilots repeatable, but it does not replace them. For each external
service, follow the [service-owner pilot guide](pilot-guide.md) and record the following without
committing proprietary image names, logs, or configuration:

```text
Service/runtime:
Container platform:
Docker or Podman version:
First setup duration:
Draincheck verdict:
Application defect found:
Diagnostic hint led to fix:
False failure or orchestration issue:
Would the team make this a release gate:
```

Use the JSON report and debug bundle as private evidence when investigating a failure. Publish only
an anonymized result after the participating team approves it. The guide links to a structured
public feedback form and explains what must remain private.
