# Contributing to Draincheck

Thanks for helping improve Draincheck. Changes should preserve its central promise: deterministic,
provider-neutral lifecycle evidence against the exact container image that will be shipped.

## Before starting

- Use the Go version and toolchain declared in `go.mod`.
- Use Docker Engine 28.0+ or Podman 4.9+ for lifecycle tests.
- Open an issue before beginning a large feature, new workload adapter, configuration-version
  change, or report-schema change so the compatibility impact can be discussed first.
- Report suspected vulnerabilities through the private process in [SECURITY.md](SECURITY.md), not
  through a public issue or pull request.

## Development checks

Run the complete Linux/CI quality gate before submitting a change:

```bash
make check
```

This verifies modules and formatting, runs `go vet`, Staticcheck, race-enabled tests,
Govulncheck, and a final build. The tools are pinned in `go.mod`; no global linter installation is
required. Windows contributors without Make or a CGO compiler can use the PowerShell sequence in
the [README development section](README.md#development). Linux CI remains authoritative for the
race detector.

Changes involving container orchestration, signals, readiness, traffic, or cleanup must also pass
the runtime conformance suite:

```bash
make e2e RUNTIME=docker
make e2e RUNTIME=podman
```

If configuration structs or schema annotations change, regenerate and commit the schema:

```bash
go run ./cmd/draincheck schema --output schema/draincheck.schema.json
git diff --exit-code schema/draincheck.schema.json
```

## Fixtures and lifecycle tests

- Keep fixtures deterministic, bounded, and independent of external services.
- Confirm traffic is active when signal delivery is acknowledged; a test that misses this barrier
  does not prove request draining.
- State the exact expected assertion failures for intentionally broken fixtures.
- Verify JSON, JUnit, and debug-bundle evidence when behavior affects reporting.
- Assert that no container carrying the run label remains after pass, failure, timeout, or
  interruption.
- Never add cleanup based on broad names, ancestor globs, or unverified IDs.

Run a focused test repeatedly when changing timing-sensitive behavior. Prefer explicit event
barriers and contexts over arbitrary sleeps.

## Compatibility and documentation

Configuration version 1 and JSON report schema 1 are stable v0.x automation interfaces. Additive
changes require tests and documentation; breaking changes require a new schema version. Update
`documentation/content/docs/support.md` when the support boundary changes and
`documentation/content/docs/troubleshooting.md` when an assertion
or diagnosis changes.

Keep pull requests focused. Include:

- The user-visible problem and the intended behavior.
- Tests demonstrating the change and failure mode.
- Runtime/version evidence for Docker or Podman behavior when relevant.
- Documentation and committed schema updates when applicable.

By contributing to this repository, you agree that your contribution is licensed under the
[Apache License 2.0](LICENSE).
