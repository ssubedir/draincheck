---
title: Pilot Draincheck in a service pipeline
description: Add Draincheck to a real service pipeline and evaluate whether it should become a release gate.
---

This guide is for a service owner evaluating Draincheck against the same container image their
pipeline may release. The first run should take about ten minutes when the application already has
an HTTP, gRPC, or in-container exec readiness check and safe work that lasts two to five seconds.

A pilot is successful when Draincheck runs repeatedly in CI, produces useful evidence on failure,
and the service owner can decide whether to make it a release gate. A passing run alone is not the
goal.

## Before you start

The pilot needs:

- A Linux container image built on the same CI runner that will execute Draincheck.
- Docker Engine 28.0 or newer, or Podman 4.9 or newer, available locally on that runner.
- The application's traffic port and an HTTP readiness path, standard gRPC health service, or fast
  health command already present in the image.
- A safe, repeatable HTTP or unary gRPC request that remains active for two to five seconds.
- Only the non-secret environment variables required for the container to start.

Use a request backed by local or disposable dependencies. Do not call production services, mutate
production data, or place credentials in `draincheck.yaml`. Built-in HTTP and gRPC probes are
plaintext; see the [support contract](support.md) for the complete boundary.

## 1. Create the configuration

Install the pinned pilot version and confirm it is the binary on `PATH`:

```bash
go install github.com/ssubedir/draincheck/cmd/draincheck@v0.2.0
draincheck version
```

Release archives are also available from the
[v0.2.0 release](https://github.com/ssubedir/draincheck/releases/tag/v0.2.0). Verify the archive
against its published `SHA256SUMS` before installing it; the
[GitHub Actions example](https://github.com/ssubedir/draincheck/blob/main/examples/github-actions.yaml)
contains copy-paste commands.

From the application repository, generate a configuration and then edit it. The committed
[starter configuration](https://github.com/ssubedir/draincheck/blob/main/examples/draincheck.yaml)
shows the completed shape with comments:

```bash
draincheck init --image checkout:local --port 8080
```

Set these application-specific values:

1. `target.image` to the local image name built by CI. The positional image passed to `verify`
   overrides this value.
2. `target.container_port` to the application's default probe port. Set the optional
   `readiness.container_port`, `traffic.container_port`, or streaming probe port only for separate
   listeners.
3. HTTP readiness users set `readiness.path`; gRPC users select `readiness.driver: grpc` and set the
   optional standard health service name; exec users select `readiness.driver: exec` and provide an
   argument vector under `readiness.exec.command`. The check must stop reporting ready promptly
   after termination begins.
4. Configure a safe HTTP request or unary gRPC method that remains active long enough for
   Draincheck to send the shutdown signal while work is in flight.

Add only required non-secret values under `target.environment`. Keep the initial assertion values
unless the application intentionally uses a different signal, clean exit code, or shutdown budget.
Changing an assertion to describe actual production policy is valid; weakening it merely to obtain
a pass is not.

Validate the file without starting a container:

```bash
draincheck validate --config draincheck.yaml
```

## 2. Prove the lifecycle locally

Build the release-style image and run Draincheck against that exact local tag:

```bash
docker build -t checkout:local .
draincheck verify checkout:local \
  --config draincheck.yaml \
  --report-json reports/draincheck.json \
  --report-junit reports/draincheck.xml \
  --debug-bundle reports/draincheck-debug.zip
```

Use `--runtime podman` when Podman is the intended runner. Do not use `--keep-on-failure` in CI;
normal runs remove the exact test container even after a failure or interruption.

The exit status separates lifecycle failures from setup problems:

| Exit | Meaning | First place to look |
|---:|---|---|
| `0` | Every lifecycle assertion passed | JSON timeline and durations |
| `1` | The lifecycle ran and one or more assertions failed | Failed JUnit cases and assertion hints |
| `2` | Configuration or command usage is invalid | Console output and resolved configuration |
| `3` | Runtime, cleanup, reporting, or another execution step failed | Debug bundle and container logs |
| `130` | Draincheck was interrupted and attempted cleanup | Interruption and cleanup events |

The debug bundle scrubs configured request-header values and secret-like environment variables,
but it can still contain application logs. Treat it as internal CI evidence and inspect it before
sharing.

## 3. Add the CI pilot

Start on a branch or pull request and keep the reports even when Draincheck fails:

- [GitHub Actions example](https://github.com/ssubedir/draincheck/blob/main/examples/github-actions.yaml)
- [GitLab CI example](https://github.com/ssubedir/draincheck/blob/main/examples/gitlab-ci.yaml)

Both examples build the application image on the runner, execute Draincheck, and retain JSON,
JUnit, and debug evidence. They pin Draincheck to `v0.2.0`; upgrade the pin deliberately after
reviewing release notes and checksums.

During the observation period, do not make the GitHub status check required; alternatively, add
`continue-on-error: true` to its job. For GitLab, add `allow_failure: true` to the job. Remove that
exception when the team adopts Draincheck as a release gate.

Run at least three times, including one run after changing application code or its base image. Make
the job blocking only after the team agrees that:

- The scenario uses the intended production shutdown signal and exit policy.
- The request is active when the signal is delivered and completes successfully.
- Readiness is withdrawn within the service's real routing budget.
- Failures point to an actionable application or container-lifecycle problem.
- No unexplained false failure or leaked container remains.

## 4. Record the decision

Record setup time, the initial verdict, whether the diagnostic evidence led to a fix, any false
failure, and whether the team would use Draincheck as a release gate. Submit the public
[pilot feedback form](https://github.com/ssubedir/draincheck/issues/new?template=pilot-feedback.yml)
only when the answers contain no proprietary identifiers, configuration, logs, credentials, or
vulnerability details.

If the evidence cannot be shared publicly, keep it inside the participating team and provide only
an approved anonymized summary to the maintainers. Security issues belong in the private process
described by the [security policy](https://github.com/ssubedir/draincheck/security/policy), never in
the pilot form.

Removing the pilot is safe: delete the CI job and `draincheck.yaml`. Draincheck does not install an
agent, contact a hosted service, modify the image, or leave a passing test container running.
