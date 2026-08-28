'use client';

import { ChevronDown, Code2, ExternalLink, FileText, MessageSquareText } from 'lucide-react';
import { Popover, PopoverContent, PopoverTrigger } from 'fumadocs-ui/components/ui/popover';
import { buttonVariants } from 'fumadocs-ui/components/ui/button';
import { cn } from '@/lib/cn';
import { withBasePath } from '@/lib/shared';

type ViewOptionsPopoverProps = {
  githubUrl: string;
  markdownUrl: string;
  pageUrl: string;
};

export function ViewOptionsPopover({
  githubUrl,
  markdownUrl,
  pageUrl,
}: ViewOptionsPopoverProps) {
  const question = `Read ${pageUrl}, I want to ask questions about it.`;
  const items = [
    {
      title: 'Open in GitHub',
      href: githubUrl,
      icon: Code2,
    },
    {
      title: 'View as Markdown',
      href: withBasePath(markdownUrl),
      icon: FileText,
    },
    {
      title: 'Open in Scira AI',
      href: `https://scira.ai/?${new URLSearchParams({ q: question })}`,
      icon: MessageSquareText,
    },
    {
      title: 'Open in ChatGPT',
      href: `https://chatgpt.com/?${new URLSearchParams({ prompt: question, hints: 'search' })}`,
      icon: MessageSquareText,
    },
    {
      title: 'Open in Claude',
      href: `https://claude.ai/new?${new URLSearchParams({ q: question })}`,
      icon: MessageSquareText,
    },
    {
      title: 'Open in Cursor',
      href: `https://cursor.com/link/prompt?${new URLSearchParams({ text: question })}`,
      icon: MessageSquareText,
    },
  ];

  return (
    <Popover>
      <PopoverTrigger
        className={cn(
          buttonVariants({ color: 'secondary', size: 'sm' }),
          'gap-2 data-[popup-open]:bg-fd-accent data-[popup-open]:text-fd-accent-foreground',
        )}
      >
        Open
        <ChevronDown className="size-3.5 text-fd-muted-foreground" aria-hidden="true" />
      </PopoverTrigger>
      <PopoverContent className="flex flex-col">
        {items.map(({ title, href, icon: Icon }) => (
          <a
            key={title}
            href={href}
            rel="noreferrer noopener"
            target="_blank"
            className="inline-flex items-center gap-2 rounded-lg p-2 text-sm hover:bg-fd-accent hover:text-fd-accent-foreground [&_svg]:size-4"
          >
            <Icon aria-hidden="true" />
            {title}
            <ExternalLink className="ms-auto size-3.5 text-fd-muted-foreground" aria-hidden="true" />
          </a>
        ))}
      </PopoverContent>
    </Popover>
  );
}
