import { useState } from 'react';
import { useAtom, useSetAtom } from 'jotai';
import { Plus, Settings, AlertCircle } from 'lucide-react';
import { activeSpaceIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { useSpaces, type SpaceInfo } from '@/lib/api/spaces';
import { tone, glyph, type SpaceTone } from '@/lib/space-visual';
import { ApiError } from '@/lib/api/client';
import { SimpleTooltip } from '@/components/ui/Tooltip';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from '@/components/ui/ContextMenu';
import { CreateSpaceDialog } from '@/components/spaces/CreateSpaceDialog';
import { DeleteSpaceDialog } from '@/components/spaces/DeleteSpaceDialog';
import { cn } from '@/lib/cn';

const TONE_BG: Record<SpaceTone, string> = {
  accent: 'bg-accent text-background',
  success: 'bg-success text-background',
  info: 'bg-info text-background',
  destructive: 'bg-destructive text-background',
  foreground: 'bg-foreground/80 text-background',
};

/**
 * Pane 1 — vertical strip of real spaces from /v1/spaces.
 *
 * States: loading (skeleton), error (icon + tooltip), empty (just +),
 * populated.
 *
 * Selection: clicking sets activeSpaceIdAtom. Right-click opens a
 * context menu with Delete….
 */
export function SpacesRail() {
  const setFocused = useSetAtom(focusedPaneAtom);
  const spacesQuery = useSpaces();
  const [createOpen, setCreateOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<SpaceInfo | null>(null);

  return (
    <nav
      aria-label="Spaces"
      data-pane="1"
      onFocus={() => setFocused(1)}
      className="flex h-full flex-col items-center justify-between bg-foreground/[0.03] py-3"
    >
      <ul className="flex flex-col items-center gap-2 overflow-y-auto">
        {spacesQuery.isPending && <SkeletonRail />}
        {spacesQuery.isError && <RailError error={spacesQuery.error} />}
        {spacesQuery.isSuccess &&
          spacesQuery.data.map((space) => (
            <li key={space.id}>
              <SpaceIcon space={space} onDelete={() => setPendingDelete(space)} />
            </li>
          ))}
        <li>
          <SimpleTooltip text="New space" side="right">
            <button
              type="button"
              aria-label="New space"
              onClick={() => setCreateOpen(true)}
              className={cn(
                'flex h-9 w-9 items-center justify-center rounded-lg',
                'border border-dashed border-foreground/20 text-foreground/50',
                'hover:border-foreground/40 hover:text-foreground/80',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background',
              )}
            >
              <Plus className="h-4 w-4" aria-hidden />
            </button>
          </SimpleTooltip>
        </li>
      </ul>
      <SimpleTooltip text="Settings (PR #?)" side="right">
        <button
          type="button"
          aria-label="Settings"
          disabled
          className={cn(
            'flex h-8 w-8 items-center justify-center rounded-md text-foreground/50',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            'disabled:cursor-not-allowed disabled:opacity-60',
          )}
        >
          <Settings className="h-4 w-4" aria-hidden />
        </button>
      </SimpleTooltip>

      <CreateSpaceDialog open={createOpen} onOpenChange={setCreateOpen} />
      <DeleteSpaceDialog
        space={pendingDelete}
        onClose={() => setPendingDelete(null)}
      />
    </nav>
  );
}

function SpaceIcon({ space, onDelete }: { space: SpaceInfo; onDelete: () => void }) {
  const [activeId, setActiveId] = useAtom(activeSpaceIdAtom);
  const active = space.id === activeId;
  const muted = space.status !== 'active';
  const t = tone(space.id);
  const label = space.name?.trim() || `Untitled (${space.id.slice(0, 6)}…)`;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <SimpleTooltip text={muted ? `${label} — ${space.status}` : label} side="right">
          <button
            type="button"
            aria-label={label}
            aria-current={active ? 'page' : undefined}
            onClick={() => setActiveId(space.id)}
            className={cn(
              'flex h-9 w-9 items-center justify-center rounded-lg text-sm font-semibold',
              'transition-all',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background',
              TONE_BG[t],
              active && 'ring-2 ring-accent ring-offset-2 ring-offset-background',
              muted && 'opacity-50',
            )}
          >
            {glyph(space.name, space.id)}
          </button>
        </SimpleTooltip>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onDelete} className="text-destructive">
          Delete…
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function SkeletonRail() {
  return (
    <>
      {[0, 1, 2].map((i) => (
        <li key={i}>
          <span
            aria-hidden
            className="block h-9 w-9 animate-pulse rounded-lg bg-foreground/10"
          />
        </li>
      ))}
    </>
  );
}

function RailError({ error }: { error: unknown }) {
  const code = error instanceof ApiError ? error.code : 'unknown';
  const message =
    error instanceof Error ? error.message : 'Could not load spaces';
  return (
    <li>
      <SimpleTooltip text={`${code}: ${message}`} side="right">
        <span
          role="img"
          aria-label="Spaces unavailable"
          className="flex h-9 w-9 items-center justify-center rounded-lg bg-destructive/10 text-destructive"
        >
          <AlertCircle className="h-4 w-4" aria-hidden />
        </span>
      </SimpleTooltip>
    </li>
  );
}
