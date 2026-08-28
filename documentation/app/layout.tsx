import { RootProvider } from 'fumadocs-ui/provider/next';
import './global.css';
import { Inter } from 'next/font/google';
import type { Metadata } from 'next';
import { withBasePath } from '@/lib/shared';

const inter = Inter({
  subsets: ['latin'],
});

const siteUrl = new URL(process.env.NEXT_PUBLIC_SITE_URL ?? 'http://localhost:3000');

export const metadata: Metadata = {
  title: {
    default: 'Draincheck — lifecycle tests for container images',
    template: '%s | Draincheck',
  },
  description:
    'Test whether a built container image becomes ready, drains in-flight work, and exits cleanly in CI.',
  metadataBase: new URL(siteUrl.origin),
  icons: {
    icon: [{ url: withBasePath('/draincheck-logo.svg'), type: 'image/svg+xml' }],
  },
};

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html lang="en" className={inter.className} suppressHydrationWarning>
      <body className="flex flex-col min-h-screen">
        <RootProvider
          search={{
            options: {
              type: 'static',
              api: withBasePath('/api/search'),
            },
          }}
        >
          {children}
        </RootProvider>
      </body>
    </html>
  );
}
