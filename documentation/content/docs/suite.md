---
title: Scenario suites
description: Run multiple named lifecycle contracts against one built container image.
---

`draincheck suite` runs multiple lifecycle configurations against one built container image. It is
useful when a service needs several CI contracts—for example an HTTP request, a gRPC stream, and a
long-lived WebSocket—without combining unrelated traffic into one lifecycle run.

```bash
draincheck suite checkout:local \
  --config scenarios/http.yaml \
  --config scenarios/grpc.yaml \
  --config scenarios/websocket.yaml \
  --report-dir reports/draincheck-suite
```

The command accepts between 2 and 100 `--config` (`-c`) values. Scenarios run in declaration order
and each receives a fresh container. Draincheck resolves and optionally pulls the shared image for
the first scenario, then reuses the local image for the remaining scenarios.

## Scenario names and images

The YAML filename without `.yaml` or `.yml` is the scenario name. A name must be 1–64 characters,
start with a letter or digit, and contain only letters, digits, dots, underscores, or hyphens.
Names are unique without regard to case, so `http.yaml` and `HTTP.yaml` cannot appear in one suite.
Directory names do not affect the name.

Every configuration must resolve to the same `target.image`. Supplying an image positionally or
with `--image` overrides `target.image` in every scenario:

```bash
draincheck suite "$IMAGE" -c scenarios/http.yaml -c scenarios/grpc.yaml
```

Draincheck loads and validates every YAML file, checks names, and checks the shared image before it
contacts the container runtime. File-backed inputs in a scenario continue to resolve relative to
that scenario's YAML file.

## Execution and exit behavior

An assertion failure records that scenario as failed and the next scenario still runs. A runtime,
cleanup, report-writing, or other execution error records the current scenario and stops the suite;
remaining scenarios appear as skipped in aggregate JUnit. Containers are removed after every
scenario, including failed scenarios.

| Code | Meaning |
|---:|---|
| `0` | Every scenario completed and passed. |
| `1` | All scenarios completed, but one or more lifecycle assertions failed. |
| `2` | CLI input or any scenario configuration is invalid; no scenario starts. |
| `3` | A runtime, cleanup, reporting, or internal execution error stopped the suite. |
| `130` | Draincheck was interrupted and attempted cleanup. |

## Evidence layout

The default report directory is `reports/draincheck-suite`:

```text
reports/draincheck-suite/
├── summary.json
├── summary.xml
└── scenarios/
    ├── http/
    │   ├── draincheck.json
    │   ├── draincheck.xml
    │   └── draincheck-debug.zip
    └── grpc/
        ├── draincheck.json
        ├── draincheck.xml
        └── draincheck-debug.zip
```

`summary.json` uses suite schema version 1. It records the shared image and runtime, total duration,
requested/completed/passed/failed scenario counts, execution-error count, aggregate verdict, and
one entry per completed scenario. Each entry includes its name, configuration path, run ID,
verdict, timing evidence, failed assertion names or execution error, and artifact directory.

`summary.xml` contains one JUnit test case per requested scenario. Normal lifecycle failures use a
`failure` element, execution failures use an `error` element, and scenarios not attempted after an
execution failure use a `skipped` element. The per-scenario reports retain the ordinary single-run
report contract.

Debug bundles can contain application logs and resolved non-secret configuration. Treat them as
sensitive CI artifacts and apply the retention and access rules described in the
[troubleshooting guide](troubleshooting.md).

## CI example

Build the image once, run the suite as an ordinary pipeline step, and publish the report directory
even when the command fails:

```bash
docker build -t checkout:"$GIT_SHA" .
draincheck suite checkout:"$GIT_SHA" \
  -c draincheck/http.yaml \
  -c draincheck/grpc.yaml \
  --report-dir reports/draincheck-suite
```

Use `suite` when configurations represent different lifecycle behaviors. Use
[`repeat`](repeat.md) when the same configuration should run several times to reveal intermittent
behavior or enforce timing distributions.
