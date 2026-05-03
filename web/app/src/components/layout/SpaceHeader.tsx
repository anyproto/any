import { useState } from 'react';
import { ChevronDown, Plus } from 'lucide-react';
import {
  useSpaceMeta,
} from '@/atoms';
import { EditSpaceDialog } from '@/components/spaces';
import { Button } from '@/components/ui';
import { useSpace } from '@/lib/api/spaces';
import { cn } from '@/lib/cn';
import { SpaceAvatar } from './SpaceAvatar';
import { useRootObjectActions } from './useSpaceContentsController';

export function SpaceHeader({ spaceId }: { spaceId: string }) {
  const spaceQuery = useSpace(spaceId);
  const { createPage, isPending } = useRootObjectActions(spaceId);
  const [editOpen, setEditOpen] = useState(false);
  const { overrideName, overrideIcon } = useSpaceMeta(spaceId);

  const effectiveName = (overrideName ?? spaceQuery.data?.name)?.trim();
  const name =
    effectiveName ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);

  return (
    <>
      <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-3">
        <button
          type="button"
          aria-label="Edit space"
          onClick={() => setEditOpen(true)}
          className={cn(
            'inline-flex max-w-[14rem] items-center gap-2 rounded-md px-1.5 py-1',
            'text-sm font-semibold text-foreground hover:bg-foreground/5',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <SpaceAvatar
            spaceId={spaceId}
            name={effectiveName}
            size="sm"
            iconOverride={overrideIcon}
          />
          <span className="truncate">{name}</span>
          <ChevronDown className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
        </button>
        <Button
          size="sm"
          variant="ghost"
          disabled={isPending}
          aria-label="New page"
          onClick={() => void createPage()}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
          {isPending ? 'Creating…' : 'New'}
        </Button>
      </header>

      <EditSpaceDialog
        spaceId={editOpen ? spaceId : null}
        serverName={spaceQuery.data?.name}
        onClose={() => setEditOpen(false)}
      />
    </>
  );
}
