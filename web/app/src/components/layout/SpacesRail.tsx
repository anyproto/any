import { useMemo, useState } from 'react';
import { useAtom, useSetAtom } from 'jotai';
import { Plus, Search, Settings, AlertCircle, Pin } from 'lucide-react';
import { activeSpaceIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { useSpaces, type SpaceInfo } from '@/lib/api/spaces';
import { ApiError } from '@/lib/api/client';
import { Input } from '@/components/ui/Input';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from '@/components/ui/ContextMenu';
import { CreateSpaceDialog } from '@/components/spaces/CreateSpaceDialog';
import { DeleteSpaceDialog } from '@/components/spaces/DeleteSpaceDialog';
import { SpaceAvatar } from './SpaceAvatar';
import { AccountAvatar } from './AccountAvatar';
import { cn } from '@/lib/cn';

/**
 * Pane 1 — list of spaces.
 *
 * Layout, top → bottom:
 *   - filter input (32px)
 *   - scrollable list of full-width rows: 36px avatar + name + trailing meta
 *   - account avatar + settings cog anchored at the bottom
 *
 * Each row = a space. Click selects; right-click → Delete… (existing flow).
 * Pinned rows render a pin marker — gated on a future `pinned` flag we
 * don't have a backend for yet, so the marker is dead UI for now.
 */
export function SpacesRail() {
  const setFocused = useSetAtom(focusedPaneAtom);
  const spacesQuery = useSpaces();
  const [createOpen, setCreateOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<SpaceInfo | null>(null);
  const [filter, setFilter] = useState('');

  const filteredSpaces = useMemo(() => {
    const q = filter.trim().toLowerCase();
    const all = spacesQuery.data ?? [];
    if (q === '') return all;
    return all.filter((s) => (s.name ?? '').toLowerCase().includes(q));
  }, [filter, spacesQuery.data]);

  return (
    <nav
      aria-label="Spaces"
      data-pane="1"
      onFocus={() => setFocused(1)}
      className="flex h-full flex-col bg-foreground/[0.03]"
    >
      {/* Header row — title + new-space button */}
      <div className="flex items-center justify-between gap-2 px-3 pt-3 pb-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-foreground/50">
          Spaces
        </span>
        <button
          type="button"
          aria-label="New space"
          onClick={() => setCreateOpen(true)}
          className={cn(
            'inline-flex h-6 w-6 items-center justify-center rounded text-foreground/60',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
        </button>
      </div>

      {/* Filter input */}
      <div className="px-3 pb-2">
        <div className="relative">
          <Search
            className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground/40"
            aria-hidden
          />
          <Input
            type="text"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter spaces…"
            className="h-8 pl-7 text-xs"
            aria-label="Filter spaces"
          />
        </div>
      </div>

      {/* List */}
      <ul className="flex-1 overflow-y-auto px-2 pb-3">
        {spacesQuery.isPending && <SkeletonRows />}
        {spacesQuery.isError && <ListError error={spacesQuery.error} />}
        {spacesQuery.isSuccess && filteredSpaces.length === 0 && (
          <li className="px-2 py-3 text-center text-xs text-foreground/40">
            {filter ? 'No matches' : 'No spaces yet'}
          </li>
        )}
        {spacesQuery.isSuccess &&
          filteredSpaces.map((space) => (
            <li key={space.id}>
              <SpaceRow space={space} onDelete={() => setPendingDelete(space)} />
            </li>
          ))}
      </ul>

      {/* Bottom anchor: account + settings */}
      <div className="flex items-center justify-between gap-2 border-t border-foreground/[0.06] px-3 py-2">
        <AccountAvatar />
        <button
          type="button"
          aria-label="Settings"
          disabled
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded text-foreground/50',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            'disabled:cursor-not-allowed disabled:opacity-60',
          )}
        >
          <Settings className="h-4 w-4" aria-hidden />
        </button>
      </div>

      <CreateSpaceDialog open={createOpen} onOpenChange={setCreateOpen} />
      <DeleteSpaceDialog
        space={pendingDelete}
        onClose={() => setPendingDelete(null)}
      />
    </nav>
  );
}

interface RowExtras {
  /** Pinned to top — placeholder until a backend property exists. */
  pinned?: boolean;
  /** Unread / mention badge count — placeholder. */
  unread?: number;
}

function SpaceRow({
  space,
  onDelete,
  extras,
}: {
  space: SpaceInfo;
  onDelete: () => void;
  extras?: RowExtras;
}) {
  const [activeId, setActiveId] = useAtom(activeSpaceIdAtom);
  const active = space.id === activeId;
  const muted = space.status !== 'active';
  const label = space.name?.trim() || `Untitled (${space.id.slice(0, 6)}…)`;
  const pinned = extras?.pinned ?? false;
  const unread = extras?.unread ?? 0;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          aria-current={active ? 'page' : undefined}
          onClick={() => setActiveId(space.id)}
          className={cn(
            'group flex w-full items-center gap-2.5 rounded-lg px-2 py-1 text-left transition-colors',
            'text-foreground/85 hover:bg-foreground/5',
            active && 'bg-foreground/8 text-foreground font-medium',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <SpaceAvatar
            spaceId={space.id}
            name={space.name}
            size="md"
            muted={muted}
          />
          <span className="flex-1 truncate text-sm">{label}</span>
          {/* Trailing meta — pin or unread badge */}
          {unread > 0 ? (
            <span
              className={cn(
                'inline-flex h-5 min-w-[1.25rem] items-center justify-center rounded-full px-1.5',
                'bg-accent text-background text-xs font-medium tabular-nums',
              )}
            >
              {unread}
            </span>
          ) : pinned ? (
            <Pin className="h-4 w-4 shrink-0 text-foreground/40" aria-hidden />
          ) : null}
        </button>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onDelete} className="text-destructive">
          Delete…
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function SkeletonRows() {
  return (
    <>
      {[0, 1, 2, 3].map((i) => (
        <li key={i} className="flex items-center gap-2.5 px-2 py-1">
          <span aria-hidden className="h-9 w-9 animate-pulse rounded-lg bg-foreground/10" />
          <span aria-hidden className="h-3 flex-1 animate-pulse rounded bg-foreground/10" />
        </li>
      ))}
    </>
  );
}

function ListError({ error }: { error: unknown }) {
  const code = error instanceof ApiError ? error.code : 'unknown';
  const message = error instanceof Error ? error.message : 'Could not load spaces';
  return (
    <li
      role="img"
      aria-label="Spaces unavailable"
      className="mx-2 mt-2 flex items-start gap-2 rounded-md border border-destructive/30 bg-destructive/[0.06] p-2 text-xs"
    >
      <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-destructive" aria-hidden />
      <span className="flex-1 text-foreground/80">
        <code className="font-mono">{code}</code> — {message}
      </span>
    </li>
  );
}
