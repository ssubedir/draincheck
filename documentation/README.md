# Draincheck documentation

This directory contains the Fumadocs application and the canonical Draincheck documentation
sources. Product documentation lives under `content/docs`; repository-level contribution,
security, and implementation-plan files remain at the repository root.

## Local development

```bash
npm install
npm run dev
```

Open [http://localhost:3000](http://localhost:3000). The documentation index is available at
`/docs`, and Fumadocs supplies search, generated Open Graph cards, Markdown copy routes, and
LLM-friendly text routes.

## Validation

```bash
npm run types:check
npm run build
```

The sidebar order is declared in `content/docs/meta.json`. Every documentation page must include
frontmatter with a concise `title` and `description`. Use relative Markdown links between pages so
Fumadocs can resolve them and the repository link test can verify them.

Set `NEXT_PUBLIC_SITE_URL` to the canonical HTTPS base URL in the deployment environment so generated
Open Graph image URLs use the public documentation host. Local development defaults to
`http://localhost:3000`.

## GitHub Pages

The `Documentation Pages` workflow builds a static export from `main` and deploys
`documentation/out` to GitHub Pages. It obtains the public URL and repository base path from the
Pages environment, so the same build supports the default project URL or a later custom domain.

Before the first deployment, open the repository's **Settings → Pages** and set **Source** to
**GitHub Actions**. Push a documentation change or run the workflow manually; the deployed project
site will normally be available at `https://ssubedir.github.io/draincheck/`.

To reproduce the Pages build locally:

```bash
NEXT_PUBLIC_BASE_PATH=/draincheck \
NEXT_PUBLIC_SITE_URL=https://ssubedir.github.io/draincheck \
npm run build
```

The release packager still publishes the support contract at `docs/support.md` inside release
archives, but reads it from `content/docs/support.md`; do not create a second copy.
