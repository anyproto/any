import { useAtomValue, useSetAtom } from 'jotai';
import { ChevronDown, FileText, Folder, MoreHorizontal, Plus, Users } from 'lucide-react';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { useSpace } from '@/lib/api/spaces';
import { useCreateObject } from '@/lib/api/objects';
import { ObjectTree } from '@/components/tree/ObjectTree';
import { Button } from '@/components/ui/Button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/DropdownMenu';
import { toast } from '@/components/ui/Toast';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';

/**
 * Pane 2 — current space contents.
 *
 * Header: space name (from useSpace) + actions; + New creates a
 * root-level object.
 * Body: <ObjectTree>, fed from /v1/spaces/:s/objects/query.
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

  return (
    <section
      aria-label="Space contents"
      data-pane="2"
      onFocus={() => setFocused(2)}
      className="flex h-full flex-col bg-foreground/[0.02]"
    >
      <Header spaceId={activeSpaceId} />
      <div className="flex-1 overflow-y-auto pb-3">
        <ObjectTree spaceId={activeSpaceId} />
      </div>
    </section>
  );
}

function Header({ spaceId }: { spaceId: string }) {
  const spaceQuery = useSpace(spaceId);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const createMutation = useCreateObject(spaceId);
  const name =
    spaceQuery.data?.name?.trim() ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);

  const create = async (folder: boolean) => {
    try {
      const args = folder ? { folder: true } : {};
      const { objectId } = await createMutation.mutateAsync(args);
      setActiveObjectId(objectId);
      toast.success(folder ? 'Folder created' : 'Page created');
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create object';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-3">
      <button
        type="button"
        className={cn(
          'inline-flex max-w-[12rem] items-center gap-1.5 rounded-md px-1.5 py-1',
          'text-sm font-semibold text-foreground hover:bg-foreground/5',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <span className="truncate">{name}</span>
        <ChevronDown className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
      </button>
      <div className="flex items-center gap-1">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size="sm"
              variant="ghost"
              disabled={createMutation.isPending}
              aria-label="New object"
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
              {createMutation.isPending ? 'Creating…' : 'New'}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => void create(false)}>
              <FileText className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
              New page
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void create(true)}>
              <Folder className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
              New folder
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
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
