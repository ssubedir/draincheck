export const appName = 'Draincheck';
export const docsRoute = '/docs';
export const docsImageRoute = '/og/docs';
export const docsContentRoute = '/llms.mdx/docs';
export const basePath = process.env.NEXT_PUBLIC_BASE_PATH?.replace(/\/$/, '') ?? '';

export function withBasePath(path: string) {
  return `${basePath}${path}`;
}

export function getPublicUrl(path: string) {
  const baseUrl = process.env.NEXT_PUBLIC_SITE_URL ?? 'http://localhost:3000';
  const canonicalPath = path.endsWith('/') ? path : `${path}/`;

  return new URL(withBasePath(canonicalPath), new URL(baseUrl).origin).toString();
}

export const gitConfig = {
  user: 'ssubedir',
  repo: 'draincheck',
  branch: 'main',
};
