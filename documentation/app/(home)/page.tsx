import Link from 'next/link';
import {
  Archive,
  ArrowRight,
  Braces,
  Check,
  Container,
  FileCheck2,
  FileJson2,
  GitPullRequest,
  ShieldCheck,
  TerminalSquare,
} from 'lucide-react';

const lifecycleSteps = [
  {
    number: '01',
    title: 'Start the final image',
    body: 'Use the same entrypoint, environment, and image contents the pipeline may release.',
  },
  {
    number: '02',
    title: 'Prove readiness',
    body: 'Wait for HTTP, gRPC Health, or an image-owned command to report ready.',
  },
  {
    number: '03',
    title: 'Hold real work',
    body: 'Keep requests, streams, or repository-owned command traffic active.',
  },
  {
    number: '04',
    title: 'Terminate and drain',
    body: 'Send the configured signal and observe withdrawal, completion, and exit.',
  },
  {
    number: '05',
    title: 'Keep the evidence',
    body: 'Return a CI verdict and write machine-readable reports for every outcome.',
  },
];

const evidence = [
  {
    icon: FileJson2,
    title: 'JSON report',
    body: 'A stable automation contract with events, assertions, timings, and final runtime state.',
  },
  {
    icon: FileCheck2,
    title: 'JUnit XML',
    body: 'Assertion-level test cases that appear naturally in existing CI test-report views.',
  },
  {
    icon: Archive,
    title: 'Debug bundle',
    body: 'Bounded logs, redacted configuration, timeline, and container state in one artifact.',
  },
];

export default function HomePage() {
  return (
    <main className="flex-1 overflow-hidden">
      <section className="relative border-b border-fd-border">
        <div className="absolute inset-0 -z-10 bg-[radial-gradient(circle_at_15%_20%,color-mix(in_oklab,var(--color-fd-primary)_14%,transparent),transparent_34%),linear-gradient(to_bottom,transparent,var(--color-fd-muted)/35)]" />
        <div className="mx-auto grid max-w-6xl gap-14 px-6 py-20 md:grid-cols-[1.15fr_0.85fr] md:items-center md:py-28">
          <div className="min-w-0">
            <h1 className="max-w-3xl text-4xl font-semibold tracking-tight text-fd-foreground sm:text-6xl">
              Prove your service drains before you deploy it.
            </h1>
            <p className="mt-6 max-w-2xl text-lg leading-8 text-fd-muted-foreground">
              Draincheck starts a Docker or Podman container, holds real work across SIGTERM, and
              turns readiness withdrawal, request draining, telemetry flushes, and clean exit into
              one repeatable contract.
            </p>
            <div className="mt-9 flex flex-wrap gap-3">
              <Link
                href="/docs/getting-started"
                className="inline-flex items-center gap-2 rounded-lg bg-fd-primary px-4 py-2.5 font-medium text-fd-primary-foreground transition-opacity hover:opacity-90"
              >
                Get started
                <ArrowRight className="size-4" aria-hidden="true" />
              </Link>
              <Link
                href="https://github.com/ssubedir/draincheck"
                className="inline-flex items-center gap-2 rounded-lg border border-fd-border bg-fd-background px-4 py-2.5 font-medium text-fd-foreground transition-colors hover:bg-fd-accent"
              >
                View on GitHub
              </Link>
            </div>
            <div className="mt-7 flex flex-wrap gap-x-5 gap-y-2 text-sm text-fd-muted-foreground">
              {['Docker + Podman', 'HTTP · gRPC · commands', 'JSON · JUnit · debug bundles'].map((item) => (
                <span key={item} className="inline-flex items-center gap-1.5">
                  <Check className="size-3.5 text-fd-primary" aria-hidden="true" />
                  {item}
                </span>
              ))}
            </div>
          </div>

          <div className="min-w-0 rounded-2xl border border-fd-border bg-fd-card/85 p-3 shadow-2xl shadow-fd-primary/5 backdrop-blur">
            <div className="flex items-center gap-2 border-b border-fd-border px-3 pb-3 text-xs text-fd-muted-foreground">
              <span className="size-2 rounded-full bg-red-400" />
              <span className="size-2 rounded-full bg-amber-400" />
              <span className="size-2 rounded-full bg-emerald-400" />
              <span className="ml-2 font-mono">pipeline / lifecycle</span>
            </div>
            <pre className="overflow-x-auto p-5 text-sm leading-7 text-fd-muted-foreground">
              <code>
                <span className="text-fd-foreground">$ draincheck verify checkout:local</span>{'\n'}
                <span className="text-fd-primary">✓</span> container ready in 182ms{'\n'}
                <span className="text-fd-primary">✓</span> 4 requests active at SIGTERM{'\n'}
                <span className="text-fd-primary">✓</span> readiness withdrawn in 41ms{'\n'}
                <span className="text-fd-primary">✓</span> in-flight work completed{'\n'}
                <span className="text-fd-primary">✓</span> exited cleanly in 326ms{'\n\n'}
                <span className="font-semibold text-fd-primary">PASS</span> lifecycle contract satisfied
              </code>
            </pre>
          </div>
        </div>
      </section>

      <section className="mx-auto max-w-6xl px-6 py-16 md:py-20">
        <div className="max-w-2xl">
          <p className="text-sm font-semibold uppercase tracking-[0.18em] text-fd-primary">Why Draincheck</p>
          <h2 className="mt-3 text-3xl font-semibold tracking-tight">Shutdown behavior is part of the artifact.</h2>
          <p className="mt-4 text-fd-muted-foreground">
            Draincheck is not a load tester or orchestrator. It verifies the lifecycle behavior of
            one final image using bounded traffic and stable evidence that any pipeline can retain.
          </p>
        </div>
        <div className="mt-10 grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          {[
            {
              icon: Container,
              title: 'Final-image testing',
              body: 'Exercise the exact entrypoint, PID 1 behavior, runtime environment, and image contents.',
            },
            {
              icon: GitPullRequest,
              title: 'Pipeline native',
              body: 'Use stable exit codes plus JSON, JUnit, and bounded debug artifacts in ordinary CI jobs.',
            },
            {
              icon: Braces,
              title: 'Real work adapters',
              body: 'Drive HTTP, gRPC, streams, or a repository-owned command while termination begins.',
            },
            {
              icon: ShieldCheck,
              title: 'Exact cleanup',
              body: 'Label every resource, bind random loopback ports, and remove only the container created for the run.',
            },
          ].map(({ icon: Icon, title, body }) => (
            <article key={title} className="rounded-xl border border-fd-border bg-fd-card p-5">
              <Icon className="size-5 text-fd-primary" aria-hidden="true" />
              <h3 className="mt-4 font-semibold text-fd-foreground">{title}</h3>
              <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">{body}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="border-y border-fd-border bg-fd-muted/25">
        <div className="mx-auto max-w-6xl px-6 py-16 md:py-20">
          <div className="max-w-2xl">
            <p className="text-sm font-semibold uppercase tracking-[0.18em] text-fd-primary">One run, one decision</p>
            <h2 className="mt-3 text-3xl font-semibold tracking-tight">See the whole termination boundary.</h2>
            <p className="mt-4 leading-7 text-fd-muted-foreground">
              The useful question is not whether a container stops. It is whether it stops accepting
              work, finishes what it already owns, flushes critical signals, and exits before its
              budget expires.
            </p>
          </div>

          <ol className="mt-10 grid gap-3 md:grid-cols-2 xl:grid-cols-5">
            {lifecycleSteps.map((step) => (
              <li
                key={step.number}
                className="relative rounded-xl border border-fd-border bg-fd-background p-5 last:md:col-span-2 last:xl:col-span-1"
              >
                <span className="font-mono text-xs font-semibold text-fd-primary">{step.number}</span>
                <h3 className="mt-3 font-semibold text-fd-foreground">{step.title}</h3>
                <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">{step.body}</p>
              </li>
            ))}
          </ol>
        </div>
      </section>

      <section className="mx-auto grid max-w-6xl gap-12 px-6 py-16 md:grid-cols-[0.85fr_1.15fr] md:items-center md:py-24">
        <div>
          <p className="text-sm font-semibold uppercase tracking-[0.18em] text-fd-primary">A small, reviewable contract</p>
          <h2 className="mt-3 text-3xl font-semibold tracking-tight">Describe the behavior your service already owns.</h2>
          <p className="mt-4 leading-7 text-fd-muted-foreground">
            Keep the lifecycle expectation beside the application. Draincheck supplies strict
            defaults, validates unknown fields, and lets explicit YAML override every meaningful
            deadline or assertion.
          </p>
          <ul className="mt-7 space-y-3 text-sm text-fd-muted-foreground">
            {[
              'Point at the locally built image and its container port.',
              'Choose a readiness check and one safe unit of meaningful work.',
              'Set the termination signal, deadline, and acceptable outcomes.',
            ].map((item) => (
              <li key={item} className="flex gap-3">
                <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-fd-primary/10 text-fd-primary">
                  <Check className="size-3" aria-hidden="true" />
                </span>
                {item}
              </li>
            ))}
          </ul>
          <Link
            href="/docs/getting-started"
            className="mt-8 inline-flex items-center gap-2 font-medium text-fd-primary hover:underline"
          >
            Build your first contract
            <ArrowRight className="size-4" aria-hidden="true" />
          </Link>
        </div>

        <div className="overflow-hidden rounded-2xl border border-fd-border bg-fd-card shadow-xl shadow-fd-primary/5">
          <div className="flex items-center gap-2 border-b border-fd-border px-5 py-3 text-xs text-fd-muted-foreground">
            <TerminalSquare className="size-4 text-fd-primary" aria-hidden="true" />
            <span className="font-mono">draincheck.yaml</span>
          </div>
          <pre className="overflow-x-auto p-6 text-sm leading-7 text-fd-muted-foreground">
            <code>{`version: 1

target:
  image: checkout:local
  container_port: 8080

readiness:
  path: /ready
  startup_timeout: 10s

traffic:
  request:
    path: /work?delay=2s
  count: 4
  concurrency: 4
  shutdown_after: 200ms

shutdown:
  signal: SIGTERM
  deadline: 15s`}</code>
          </pre>
        </div>
      </section>

      <section className="border-y border-fd-border bg-fd-muted/20">
        <div className="mx-auto max-w-6xl px-6 py-16 md:py-20">
          <div className="grid gap-8 md:grid-cols-[0.7fr_1.3fr] md:items-end">
            <div>
              <p className="text-sm font-semibold uppercase tracking-[0.18em] text-fd-primary">Evidence that survives the job</p>
              <h2 className="mt-3 text-3xl font-semibold tracking-tight">A failure should explain itself.</h2>
            </div>
            <p className="max-w-2xl leading-7 text-fd-muted-foreground md:justify-self-end">
              Console output stays concise. Deeper evidence is written atomically, bounded before
              capture, and ready for the CI system your team already uses.
            </p>
          </div>
          <div className="mt-10 grid gap-4 md:grid-cols-3">
            {evidence.map(({ icon: Icon, title, body }) => (
              <article key={title} className="rounded-xl border border-fd-border bg-fd-background p-6">
                <div className="flex size-10 items-center justify-center rounded-lg bg-fd-primary/10 text-fd-primary">
                  <Icon className="size-5" aria-hidden="true" />
                </div>
                <h3 className="mt-5 font-semibold text-fd-foreground">{title}</h3>
                <p className="mt-2 text-sm leading-6 text-fd-muted-foreground">{body}</p>
              </article>
            ))}
          </div>
        </div>
      </section>

      <section className="mx-auto max-w-6xl px-6 py-16 md:py-24">
        <div className="relative overflow-hidden rounded-2xl border border-fd-primary/25 bg-fd-primary/10 px-6 py-10 md:px-10 md:py-12">
          <div className="absolute -right-20 -top-24 -z-10 size-72 rounded-full bg-fd-primary/15 blur-3xl" />
          <div className="grid gap-8 lg:grid-cols-[1fr_auto] lg:items-center">
            <div>
              <p className="text-sm font-semibold uppercase tracking-[0.18em] text-fd-primary">One CLI step. One YAML file. No control plane.</p>
              <h2 className="mt-3 text-3xl font-semibold tracking-tight">Make shutdown behavior part of your release evidence.</h2>
              <p className="mt-4 max-w-2xl leading-7 text-fd-muted-foreground">
                Start with a non-blocking service pilot, keep the reports, and promote the check only
                when the scenario exercises work your team considers meaningful.
              </p>
            </div>
            <div className="flex flex-wrap lg:justify-end">
              <Link
                href="/docs/getting-started"
                className="inline-flex items-center gap-2 rounded-lg bg-fd-primary px-4 py-2.5 font-medium text-fd-primary-foreground transition-opacity hover:opacity-90"
              >
                Get started
                <ArrowRight className="size-4" aria-hidden="true" />
              </Link>
            </div>
          </div>
        </div>
      </section>

      <footer className="border-t border-fd-border">
        <div className="mx-auto flex max-w-6xl flex-col gap-5 px-6 py-8 text-sm text-fd-muted-foreground sm:flex-row sm:items-center sm:justify-between">
          <p>Draincheck · Apache-2.0 · lifecycle tests for container images</p>
          <nav className="flex flex-wrap gap-x-5 gap-y-2" aria-label="Footer navigation">
            <Link href="/docs" className="hover:text-fd-foreground">Documentation</Link>
            <Link href="/docs/support" className="hover:text-fd-foreground">Support boundary</Link>
            <Link href="https://github.com/ssubedir/draincheck" className="hover:text-fd-foreground">GitHub</Link>
          </nav>
        </div>
      </footer>
    </main>
  );
}
