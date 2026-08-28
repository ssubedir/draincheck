---
title: Repeated lifecycle verification
description: Detect intermittent shutdown failures and enforce lifecycle timing budgets across fresh containers.
---

`draincheck repeat` runs the same lifecycle contract against fresh containers sequentially and
summarizes the timing distribution. It is intended for detecting intermittent shutdown behavior
and measuring lifecycle-budget headroom in CI. It is not a load test or an application-performance
benchmark.

The command uses the same versioned `draincheck.yaml` as `verify`:

```bash
draincheck repeat checkout:local \
  --config draincheck.yaml \
  --runtime docker \
  --runs 5 \
  --report-dir reports/draincheck-repeat
```

Optional p95 gates turn the observed timing distribution into an explicit repeat-mode CI contract:

```yaml
repeat:
  budgets:
    startup_ready_p95: 2s
    readiness_withdrawal_p95: 750ms
    container_exit_p95: 5s
```

Omit any field that should remain informational. Durations must be at least `1ms`. These settings
are accepted but ignored by `draincheck verify`; only `draincheck repeat` evaluates them.

Each run creates a new container with a unique run ID, executes the complete readiness, traffic,
signal, drain, exit, evidence, and cleanup sequence, and finishes cleanup before the next run
begins. Runs are deliberately sequential so concurrent test containers do not compete for the
runner and distort the lifecycle being measured.

The first run applies `--pull never`, `--pull missing`, or `--pull always` as requested. Later runs
use the already resolved local image without pulling again, so one repeat sequence measures one
image. The command does not expose `--keep-on-failure`; repeat mode always attempts cleanup before
continuing.

## Verdict and exit codes

Assertion failures do not stop the sequence. This makes an intermittent failure visible alongside
the other observations. An execution or reporting error stops later runs because their evidence
would not be comparable or safely writable.

| Exit | Repeat outcome |
|---:|---|
| `0` | Every requested run and configured repeat budget completed and passed. |
| `1` | Every run executed, but at least one lifecycle assertion or configured repeat budget failed. |
| `2` | The command or configuration was invalid. |
| `3` | A runtime, cleanup, evidence, or aggregate-report error stopped the sequence. |
| `130` | The command was interrupted and attempted cleanup and partial reporting. |

`--runs` defaults to `3` and accepts values from `2` through `100`. Start with three runs in a
normal pull-request job. Larger samples belong in a scheduled pipeline because every observation
executes real application work and waits for a complete shutdown.

## Evidence layout

`--report-dir` defaults to `reports/draincheck-repeat` and contains:

```text
reports/draincheck-repeat/
├── summary.json
├── summary.xml
└── runs/
    ├── run-001-<run-id>/
    │   ├── draincheck.json
    │   ├── draincheck.xml
    │   └── draincheck-debug.zip
    └── run-002-<run-id>/
        └── ...
```

The aggregate JSON uses its own `schema_version: 1`. It records requested and completed counts,
each run's verdict and failed assertion names, relative artifact directories, timing statistics,
`budget_failures`, and one entry in `budget_assertions` per configured limit. A budget entry records
its stable name, sample count, configured limit, observed p95, evaluation state, verdict, and human
message.

The aggregate JUnit file contains one case per requested run plus one case per configured budget.
An execution error is a JUnit error, a lifecycle or evaluated budget verdict is a failure, and runs
or budgets that cannot be evaluated after an execution/lifecycle failure are marked skipped.

Per-run files retain the existing single-verification JSON and JUnit contracts. Debug bundles use
the same bounded redaction and scrubbing rules as `verify` and should still be treated as internal
CI evidence because they contain application logs.

## Timing definitions

Both the single-run report and repeated summary expose these durations:

| Timing | Start | End |
|---|---|---|
| `startup_ready` | Verification begins, before image preflight | The configured HTTP success or gRPC `SERVING` state is first observed |
| `signal_delivery` | Draincheck requests the configured signal | The container runtime confirms signal delivery |
| `readiness_withdrawal` | Draincheck requests the signal | Readiness first stops returning the configured success status |
| `container_exit` | Draincheck requests the signal | Runtime inspection first observes the container stopped |
| `verification` | One verification begins | Evidence collection and cleanup finish |

The summary reports sample count, minimum, nearest-rank p50, nearest-rank p95, and maximum for
passing runs. Failed runs remain in the verdict and per-run evidence but are excluded from the
distribution because they may not reach every lifecycle milestone.

## Budget evaluation

Budget names are stable aggregate assertions:

| Configuration | Assertion | Observed timing |
|---|---|---|
| `startup_ready_p95` | `repeat.startup_ready_p95` | `timings.startup_ready.p95_ms` |
| `readiness_withdrawal_p95` | `repeat.readiness_withdrawal_p95` | `timings.readiness_withdrawal.p95_ms` |
| `container_exit_p95` | `repeat.container_exit_p95` | `timings.container_exit.p95_ms` |

An evaluated budget passes when the observed nearest-rank p95 is less than or equal to its limit.
Budgets are evaluated only when every requested lifecycle run completes and passes. If a run fails
or execution stops early, configured budget cases are reported as not evaluated and skipped in
JUnit; the lifecycle evidence remains the cause of the aggregate failure.

A budget failure does not rewrite per-run reports or increment `runs_failed`: all individual runs
may be green while `passed` is false and `budget_failures` is non-zero. This separation lets CI
distinguish correctness failures from a lifecycle timing regression.

Small CI samples show consistency and deadline headroom; they do not establish statistically
stable service-level objectives. Set budgets with enough headroom for runner variation and use
larger scheduled samples before treating p95 as a performance service-level objective.
