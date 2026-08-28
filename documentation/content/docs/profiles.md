---
title: Lifecycle profiles
description: Apply platform-aware local lifecycle defaults and Kubernetes pre-stop ordering.
---

Profiles apply documented lifecycle defaults and event ordering while Draincheck continues to run
locally against one container image. They do not deploy workloads, read platform manifests, contact
a control plane, or certify compatibility with a platform version.

Select the same profile when validating and running a scenario:

```bash
draincheck validate --config draincheck.yaml --profile kubernetes
draincheck verify checkout:local --config draincheck.yaml --profile kubernetes
draincheck repeat checkout:local --config draincheck.yaml --profile kubernetes --runs 3
draincheck suite checkout:local --profile kubernetes \
  --config scenarios/http.yaml \
  --config scenarios/grpc.yaml
```

`generic` remains the default profile. Configuration fields explicitly present in YAML override
profile defaults. When `shutdown.deadline` is omitted, `generic` supplies 15 seconds and
`kubernetes` supplies Kubernetes' default 30-second Pod termination grace period.

## Kubernetes profile

The Kubernetes profile models the single-container shutdown sequence that can be tested without a
cluster:

1. Start the shared termination grace-period clock.
2. Run the configured exec pre-stop hook inside the container.
3. Request the configured signal, `SIGTERM` by default.
4. Observe readiness withdrawal, in-flight work, streams, telemetry flushes, and container exit.
5. Require the container to exit before the original shared deadline.

This ordering follows the Kubernetes container lifecycle contract: the grace-period countdown
starts before `PreStop`, and the hook completes before TERM is sent. Kubernetes documents a default
`terminationGracePeriodSeconds` of 30 seconds. See the official
[container lifecycle hooks](https://kubernetes.io/docs/concepts/containers/container-lifecycle-hooks/)
and [Pod termination flow](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/).

Configure an image-owned exec hook under `shutdown`:

```yaml
shutdown:
  signal: SIGTERM
  # deadline: 45s # optional; overrides the profile's 30s default
  pre_stop:
    exec:
      command: ["/app/pre-stop", "--drain"]
```

Draincheck passes the command as an argument vector to `docker exec` or `podman exec`; no shell is
added. The command runs as the container's configured user and should be fast, side-effect-aware,
and idempotent. Do not put credentials in command arguments because debug configuration retains the
declared argument vector. Kubernetes hook delivery is intended to be at least once, even though
one Draincheck run invokes it once.

Exit `0` satisfies `shutdown.pre_stop`. A non-zero exit fails that assertion, but Draincheck still
requests SIGTERM so the report can retain signal, draining, exit, and cleanup evidence. This is an
intentional diagnostic behavior and is not a claim that every kubelet version reacts identically
to a failed hook. A hook that consumes the whole shared deadline prevents normal signal delivery
and requires forced cleanup.

Hook stdout and stderr are capped at 4 KiB each and excluded from reports. JSON reports record the
selected `profile`, resolved shutdown deadline, hook exit code, timeout state, hook duration, and
total shutdown duration. Repeat summaries aggregate pre-stop and total-shutdown timing across
passing runs.

## Boundary

The profile cannot reproduce control-plane EndpointSlice updates, Service or ingress propagation,
sidecar ordering, node shutdown, eviction policy, PodDisruptionBudgets, custom runtimes, or
manifest-specific hooks that were not copied into `draincheck.yaml`. Those require cluster-level
tests. Draincheck verifies the final image's local lifecycle behavior under a declared simulation.
