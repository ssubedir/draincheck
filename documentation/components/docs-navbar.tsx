'use client';

import type { ComponentProps } from 'react';
import { PanelLeft } from 'lucide-react';
import { HomeLayout } from 'fumadocs-ui/layouts/home';
import { Header as HomeHeader } from 'fumadocs-ui/layouts/home/slots/header';
import { useDocsLayout } from 'fumadocs-ui/layouts/docs';
import { baseOptions } from '@/lib/layout.shared';

function NavbarContainer({ children }: ComponentProps<'main'>) {
  return <div className="contents [--fd-layout-width:1400px]">{children}</div>;
}

function NavbarHeader(props: ComponentProps<'header'>) {
  return (
    <HomeHeader
      {...props}
      style={{ ...props.style, position: 'fixed', insetInline: 0, top: 0 }}
    />
  );
}

function DocsSidebarTrigger() {
  const { slots } = useDocsLayout();
  const SidebarTrigger = slots.sidebar?.trigger;

  if (!SidebarTrigger) return null;

  return (
    <SidebarTrigger
      aria-label="Open documentation navigation"
      className="inline-flex size-9 items-center justify-center rounded-md text-fd-muted-foreground transition-colors hover:bg-fd-accent hover:text-fd-accent-foreground md:hidden"
    >
      <PanelLeft className="size-4" aria-hidden="true" />
    </SidebarTrigger>
  );
}

export function DocsNavbar() {
  const options = baseOptions();

  return (
    <HomeLayout
      {...options}
      nav={{ ...options.nav, children: <DocsSidebarTrigger /> }}
      slots={{ container: NavbarContainer, header: NavbarHeader }}
    />
  );
}
