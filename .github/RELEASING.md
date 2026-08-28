# Releasing Draincheck

This is the maintainer runbook for validating, signing, and publishing Draincheck releases.

Draincheck publishes static Linux binaries for `amd64` and `arm64`. The release candidate is
lifecycle-tested against Docker before publication. Native macOS and Windows binaries remain
unpublished: exercising Linux containers through Docker Desktop or another runtime-managed VM does
not validate native packaging, process behavior, filesystem paths, signals, and upgrades on those
operating systems.

The tag-triggered workflow validates, builds, tests, signs, and publishes everything. Do not create
release assets manually.

## Repository requirements

Keep these requirements in place for every release:

1. Keep the checked-in project copy of the Apache License 2.0 unchanged. The release workflow
   verifies it, and deterministic archives include a copy automatically.
2. Ensure GitHub Actions can write repository contents using `GITHUB_TOKEN`.
3. Enable release immutability in the repository settings so published tags and assets cannot be
   replaced.
4. Enable GitHub private vulnerability reporting and confirm the **Report a vulnerability** button
   described in the repository [security policy](https://github.com/ssubedir/draincheck/security/policy)
   is available.
5. Review the [support contract](../documentation/content/docs/support.md) against the fixed Ubuntu
   runner labels and the runtime-version evidence from the latest lifecycle and pilot workflows.

Checksum signing is keyless. GitHub's OIDC identity is used for the checksum manifest, so there is
no long-lived signing key or signing secret to provision.

## Tested release boundary

The tag workflow does more than compile the CLI. Before any publish job receives write access, its
`validate` job runs dependency verification, formatting, static analysis, race-enabled tests,
vulnerability scanning, and the schema-drift check. It then runs `make dogfood`, which:

1. Builds a release-metadata-stamped Draincheck binary.
2. Builds the committed `good-http` fixture as a Linux image with Docker.
3. Runs a complete lifecycle verification against that image with Docker.
4. Writes JSON, JUnit, and debug-bundle evidence and uploads it even when validation fails.

The normal CI runtime matrix separately exercises Docker and Podman. This evidence supports the
documented local Linux runtime boundary; it is not evidence for native Windows or macOS release
artifacts, Windows containers, remote daemons, or every Docker Desktop version.

## Local dry run

From a clean checkout, build both release archives and the checksum manifest without publishing:

```bash
RELEASE_TAG=v0.2.0
make release-dry-run RELEASE_VERSION="${RELEASE_TAG}"
sha256sum --check dist/SHA256SUMS
```

The build uses the current commit timestamp, embeds version/commit/date metadata, disables CGO and
VCS auto-stamping, and normalizes archive ownership, permissions, ordering, and timestamps. Running
the command twice against the same commit and toolchain should produce identical SHA-256 hashes.

The equivalent command without Make is:

```bash
RELEASE_TAG=v0.2.0
go run ./tools/release package \
  --version "${RELEASE_TAG}" \
  --commit "$(git rev-parse HEAD)" \
  --date "$(git show -s --format=%cI HEAD)" \
  --output dist
go run ./tools/release checksums --output dist
```

On PowerShell, set the version with `$releaseTag = 'v0.2.0'` and run
`make release-dry-run RELEASE_VERSION=$releaseTag`.

Local dry runs do not sign artifacts because the trusted GitHub Actions OIDC identity exists only in
the release workflow.

## Publishing

Only a pushed, v-prefixed semantic version tag starts a release. The tagged commit must be reachable
from `main`, and the workflow reruns tests, schema drift checks, and the dogfood lifecycle before
granting any publish job write access.

After `main` CI is green:

```bash
RELEASE_TAG=v0.2.0
git switch main
git pull --ff-only
git tag -s "${RELEASE_TAG}" -m "Draincheck ${RELEASE_TAG}"
git push origin "${RELEASE_TAG}"
```

Draincheck's published `v0.1.0` and `v0.1.1` tags use SSH signatures. Configure Git once with the
public key that corresponds to your private SSH signing key:

```bash
git config --global gpg.format ssh
git config --global user.signingkey ~/.ssh/id_ed25519.pub
```

Before pushing, inspect the tag object with `git cat-file tag "${RELEASE_TAG}"`; it should contain an
SSH signature. If you have configured Git's SSH allowed-signers file, `git tag -v "${RELEASE_TAG}"`
also verifies the signer identity. If Git reports `No secret key` while creating the tag, stop and
correct the signing configuration; do not replace the signed tag with a lightweight or unsigned tag.

Pushing the tag is the publication boundary. The workflow then:

1. Runs the full validation suite, schema-drift check, and Docker lifecycle dogfood test.
2. Builds Linux `amd64` on `ubuntu-24.04` and Linux `arm64` on `ubuntu-24.04-arm`.
3. Executes each binary on its native architecture and validates `version`, `schema`, and the
   committed dogfood configuration.
4. Creates provenance attestations for both binary archives.
5. Generates architecture-specific SPDX JSON SBOMs, `SHA256SUMS`, and a keyless Sigstore bundle for
   the checksum manifest.
6. Creates a draft GitHub release, attaches every asset, and publishes the completed draft.

Prerelease tags such as `v0.2.0-rc.1` produce a GitHub prerelease.

## Published assets

Each GitHub release contains:

| Asset | Purpose |
|---|---|
| `draincheck_linux_amd64.tar.gz` | Static Linux amd64 binary, schema, README, support contract, and license. |
| `draincheck_linux_arm64.tar.gz` | Static Linux arm64 binary, schema, README, support contract, and license. |
| `draincheck_linux_*.spdx.json` | Architecture-specific SPDX SBOMs. |
| `SHA256SUMS` | Hashes for the archives and SBOMs. |
| `SHA256SUMS.sigstore.json` | Keyless Cosign signature bundle for the checksum manifest. |

GitHub stores build-provenance and SBOM attestations separately from the downloadable assets.

## Verifying downloads

The examples below use `v0.2.0`. Replace it with the exact tag you downloaded.

Verify hashes first:

```bash
sha256sum --check SHA256SUMS
```

Verify the keyless checksum signature for a specific release tag:

```bash
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.sigstore.json \
  --certificate-identity \
    "https://github.com/ssubedir/draincheck/.github/workflows/release.yml@refs/tags/v0.2.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```

Verify GitHub build provenance for an archive:

```bash
gh attestation verify draincheck_linux_amd64.tar.gz \
  --repo ssubedir/draincheck \
  --source-ref refs/tags/v0.2.0 \
  --signer-workflow ssubedir/draincheck/.github/workflows/release.yml
```

An attestation proves which source and workflow produced an artifact; it does not by itself prove
that the software is vulnerability-free or appropriate for a particular environment.

## Failure and recovery

The GitHub release is created as a draft and published only after the Docker lifecycle dogfood test,
every build, native smoke test, SBOM, and checksum succeeds. If the workflow fails before the final
step, inspect the failed job and rerun it after addressing transient infrastructure problems.

Never move or reuse a published version tag. If an artifact is incorrect after publication, fix the
source and release a new patch version.

A patch release must also preserve the configuration, JSON report, JUnit, CLI, and exit-code
guarantees in the [support contract](../documentation/content/docs/support.md). If a proposed change cannot meet that contract,
defer it to a versioned migration rather than weakening an existing v0.x interface.
