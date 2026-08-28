import { source } from '@/lib/source';
import { DocsLayout } from 'fumadocs-ui/layouts/docs';
import { baseOptions } from '@/lib/layout.shared';
import { DocsNavbar } from '@/components/docs-navbar';

export default function Layout({ children }: LayoutProps<'/docs'>) {
  const options = baseOptions();

  return (
    <DocsLayout
      tree={source.getPageTree()}
      {...options}
      links={[]}
      githubUrl={undefined}
      nav={{ ...options.nav, component: <DocsNavbar /> }}
      containerProps={{
        className: 'docs-with-site-navbar',
        style: {
          '--fd-header-height': '3.5rem',
          paddingTop: 'var(--fd-header-height)',
        } as React.CSSProperties,
      }}
    >
      {children}
    </DocsLayout>
  );
}
