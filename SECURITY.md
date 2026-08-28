# Security policy

Draincheck runs beside a privileged container runtime and consumes untrusted configuration,
container metadata, logs, and HTTP responses. Please report behavior that could cross those trust
boundaries privately.

## Supported versions

Security fixes are developed on `main`. Only the latest release line is supported; users of older
releases may be asked to upgrade before receiving a fix.

## Reporting a vulnerability

Do not include vulnerability details, proof-of-concept payloads, secrets, or exploit output in a
public issue or pull request.

The preferred channel is GitHub private vulnerability reporting:

1. Open the repository's **Security** tab.
2. Select **Advisories** and then **Report a vulnerability**.
3. Provide the affected version or commit, impact, reproduction steps, and any suggested fix or
   mitigation.

If the **Report a vulnerability** button is unavailable, open a public issue containing only
`Security contact requested` and no technical details. A maintainer will establish a private
channel before asking for the report.

Useful reports include:

- Command or argument injection through image names, configuration, runtime output, or reports.
- Cleanup that can remove a container other than the exact one Draincheck created.
- Secret leakage through terminal output, JSON, JUnit, debug bundles, or container logs.
- Unsafe archive extraction, release verification, signature, or provenance behavior.
- Privilege escalation or unintended access to the Docker or Podman runtime.
- Denial-of-service issues caused by unbounded untrusted input.

You should receive an acknowledgment within seven days. This is a project goal, not a service-level
agreement. Please allow time for triage and a coordinated fix before public disclosure. Maintainers
will credit reporters unless anonymity is requested.
