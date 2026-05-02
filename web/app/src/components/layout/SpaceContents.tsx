import { useState } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import { ChevronDown, ChevronRight, MoreHorizontal, Users } from 'lucide-react';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import {
  MOCK_SPACES,
  mockSectionsForSpace,
  type MockItem,
  type MockSection,
} from '@/lib/mock-data';
import { cn } from '@/lib/cn';

/**
 * Pane 2 — current space contents.
 *
 * Header (space name + members count placeholder + ▾) above a sectioned
 * list. Selecting a row writes activeObjectIdAtom; pane 3 reads it.
 *
 * Empty state: when no active space, prompts to pick one. PR #3 picks
 * the first space automatically when /v1/spaces returns ≥ 1 row.
 */
export function SpaceContents() {
  const activeSpaceId = useAtomValue(activeSpaceIdAtom);
  const setFocused = useSetAtom(focusedPaneAtom);

  if (!activeSpaceId) {
    return (
      <section
        data-pane="2"
        onFocus={() => setFocused(2)}
        className="flex h-full items-center justify-center p-4"
      >
        <p className="text-sm text-foreground/60">Pick a space on the left.</p>
      </section>
    );
  }

  const space = MOCK_SPACES.find((s) => s.id === activeSpaceId);
  const sections = mockSectionsForSpace(activeSpaceId);

  return (
    <section
      aria-label={`Contents of ${space?.name ?? 'space'}`}
      data-pane="2"
      onFocus={() => setFocused(2)}
      className="flex h-full flex-col bg-foreground/[0.02]"
    >
      <Header name={space?.name ?? activeSpaceId} />
      <div className="flex-1 overflow-y-auto px-2 pb-3">
        {sections.map((section) => (
          <SectionView key={section.id} section={section} />
        ))}
      </div>
    </section>
  );
}

function Header({ name }: { name: string }) {
  return (
    <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-3">
      <button
        type="button"
        className={cn(
          'inline-flex max-w-[14rem] items-center gap-1.5 rounded-md px-1.5 py-1',
          'text-sm font-semibold text-foreground hover:bg-foreground/5',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <span className="truncate">{name}</span>
        <ChevronDown className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
      </button>
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-label="Members"
          className="rounded-md p-1.5 text-foreground/60 hover:bg-foreground/5 hover:text-foreground"
        >
          <Users className="h-3.5 w-3.5" aria-hidden />
        </button>
        <button
          type="button"
          aria-label="Space menu"
          className="rounded-md p-1.5 text-foreground/60 hover:bg-foreground/5 hover:text-foreground"
        >
          <MoreHorizontal className="h-3.5 w-3.5" aria-hidden />
        </button>
      </div>
    </header>
  );
}

function SectionView({ section }: { section: MockSection }) {
  const [collapsed, setCollapsed] = useState(section.defaultCollapsed ?? false);
  const headerProps = section.collapsible
    ? {
        as: 'button' as const,
        onClick: () => setCollapsed((v) => !v),
      }
    : { as: 'div' as const };

  return (
    <div className="mt-3 first:mt-1">
      <SectionHeader
        label={section.label}
        collapsible={!!section.collapsible}
        collapsed={collapsed}
        onClick={'onClick' in headerProps ? headerProps.onClick : undefined}
      />
      {!collapsed && (
        <ul className="mt-1">
          {section.items.map((item) => (
            <li key={item.id}>
              <ItemRow item={item} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function SectionHeader({
  label,
  collapsible,
  collapsed,
  onClick,
}: {
  label: string;
  collapsible: boolean;
  collapsed: boolean;
  onClick?: (() => void) | undefined;
}) {
  const Icon = collapsed ? ChevronRight : ChevronDown;
  if (!collapsible) {
    return (
      <div className="px-2 py-1 text-xs font-medium uppercase tracking-wide text-foreground/40">
        {label}
      </div>
    );
  }
  return (
    <button
      type="button"
      onClick={onClick}
      aria-expanded={!collapsed}
      className={cn(
        'flex w-full items-center gap-1 px-2 py-1 text-xs font-medium uppercase tracking-wide',
        'text-foreground/40 hover:text-foreground/60',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      <Icon className="h-3 w-3" aria-hidden />
      {label}
    </button>
  );
}

function ItemRow({ item }: { item: MockItem }) {
  const activeObjectId = useAtomValue(activeObjectIdAtom);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const active = activeObjectId === item.id;

  return (
    <button
      type="button"
      onClick={() => setActiveObjectId(item.id)}
      aria-current={active ? 'page' : undefined}
      className={cn(
        'group flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-foreground',
        'hover:bg-foreground/5',
        active && 'bg-foreground/8 font-medium',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      <span className="text-base leading-none" aria-hidden>
        {item.glyph ?? '·'}
      </span>
      <span className="flex-1 truncate text-left">{item.title}</span>
      {item.badge && (
        <span
          className={cn(
            'inline-flex h-5 min-w-[1.25rem] items-center justify-center rounded-full px-1.5 text-xs font-medium tabular-nums',
            item.badge.kind === 'mention'
              ? 'bg-accent text-background'
              : 'bg-foreground/15 text-foreground/80',
          )}
        >
          {item.badge.count}
        </span>
      )}
    </button>
  );
}
