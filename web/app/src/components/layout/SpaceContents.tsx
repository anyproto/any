import { useState } from 'react';
import { useAtomValue, useSetAtom } from 'jotai';
import { useQuery } from '@tanstack/react-query';
import {
  ChevronDown,
  FileText,
  Folder,
  MoreHorizontal,
  Plus,
  Settings2,
  Sparkles,
  Users,
} from 'lucide-react';
import { activeSpaceIdAtom, activeObjectIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { useSpace } from '@/lib/api/spaces';
import { useCreateObject, queryObjects } from '@/lib/api/objects';
import { useTypes, type TypeInfo } from '@/lib/api/types';
import { ObjectTree } from '@/components/tree/ObjectTree';
import { Button } from '@/components/ui/Button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/DropdownMenu';
import { toast } from '@/components/ui/Toast';
import { CreateTypeDialog } from '@/components/types/CreateTypeDialog';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';
import { SpaceAvatar } from './SpaceAvatar';
import { SectionHeader } from './SectionHeader';

/**
 * Pane 2 — current space contents.
 *
 * Header: small space avatar + name + chevron + actions.
 * Body: two sections — Pages (the tree, expanded) and Types (collapsed).
 */
export function SpaceContents() {
  const activeSpaceId = useAtomValue(activeSpaceIdAtom);
  const setFocused = useSetAtom(focusedPaneAtom);
  const [pagesOpen, setPagesOpen] = useState(true);
  const [typesOpen, setTypesOpen] = useState(false);

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
        <div className="mt-2">
          <PagesSection
            spaceId={activeSpaceId}
            expanded={pagesOpen}
            onToggle={() => setPagesOpen((v) => !v)}
          />
        </div>
        <div className="mt-3">
          <TypesSection
            spaceId={activeSpaceId}
            expanded={typesOpen}
            onToggle={() => setTypesOpen((v) => !v)}
          />
        </div>
      </div>
    </section>
  );
}

function Header({ spaceId }: { spaceId: string }) {
  const spaceQuery = useSpace(spaceId);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const createMutation = useCreateObject(spaceId);
  const typesQuery = useTypes(spaceId);
  const [createTypeOpen, setCreateTypeOpen] = useState(false);

  const name =
    spaceQuery.data?.name?.trim() ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);
  const userTypes = (typesQuery.data ?? []).filter((t) => !t.builtIn);

  const create = async (
    args: { folder?: boolean; typeIds?: string[]; label: string } = { label: 'page' },
  ) => {
    try {
      const opts: { folder?: boolean; typeIds?: string[] } = {};
      if (args.folder) opts.folder = true;
      if (args.typeIds && args.typeIds.length > 0) opts.typeIds = args.typeIds;
      const { objectId } = await createMutation.mutateAsync(opts);
      setActiveObjectId(objectId);
      toast.success(`Created ${args.label}`);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create object';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <>
      <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-3">
        <button
          type="button"
          className={cn(
            'inline-flex max-w-[14rem] items-center gap-2 rounded-md px-1.5 py-1',
            'text-sm font-semibold text-foreground hover:bg-foreground/5',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <SpaceAvatar spaceId={spaceId} name={spaceQuery.data?.name} size="sm" />
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
              <DropdownMenuItem onSelect={() => void create({ label: 'page' })}>
                <FileText className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                New page
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => void create({ folder: true, label: 'folder' })}>
                <Folder className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                New folder
              </DropdownMenuItem>
              {userTypes.length > 0 && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuLabel>Custom types</DropdownMenuLabel>
                  {userTypes.map((t) => {
                    const label = t.name?.trim() || `Untitled (${t.id.slice(0, 6)}…)`;
                    return (
                      <DropdownMenuItem
                        key={t.id}
                        onSelect={() => void create({ typeIds: [t.id], label })}
                      >
                        <Sparkles className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                        New {label}
                      </DropdownMenuItem>
                    );
                  })}
                </>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => setCreateTypeOpen(true)}>
                <Settings2 className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                Create type…
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

      <CreateTypeDialog open={createTypeOpen} onOpenChange={setCreateTypeOpen} />
    </>
  );
}

/**
 * Pages section — same chevron pattern as TypesSection, but its label
 * is the space name (acts as the "root" header). Body is the existing
 * <ObjectTree>.
 */
function PagesSection({
  spaceId,
  expanded,
  onToggle,
}: {
  spaceId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const spaceQuery = useSpace(spaceId);
  const label =
    spaceQuery.data?.name?.trim() ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);
  return (
    <>
      <SectionHeader label={label} expanded={expanded} onToggle={onToggle} />
      {expanded && <ObjectTree spaceId={spaceId} />}
    </>
  );
}

/**
 * Real `/v1/types` filtered to user types. Object counts via one
 * cross-object query per visible type — fine at the handful-of-types
 * scale; if a user creates dozens we batch later (flagged in spec).
 */
function TypesSection({
  spaceId,
  expanded,
  onToggle,
}: {
  spaceId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const typesQuery = useTypes(spaceId);
  const userTypes = (typesQuery.data ?? []).filter((t) => !t.builtIn);

  return (
    <>
      <SectionHeader
        label="Types"
        count={userTypes.length || undefined}
        expanded={expanded}
        onToggle={onToggle}
      />
      {expanded && (
        <ul className="mt-1 px-1">
          {userTypes.length === 0 ? (
            <li className="px-3 py-2 text-xs text-foreground/40">
              No custom types yet.
            </li>
          ) : (
            userTypes.map((t) => <TypeRow key={t.id} spaceId={spaceId} type={t} />)
          )}
        </ul>
      )}
    </>
  );
}

function TypeRow({ spaceId, type }: { spaceId: string; type: TypeInfo }) {
  const label = type.name?.trim() || `Untitled (${type.id.slice(0, 6)}…)`;
  const count = useTypeObjectCount(spaceId, type.id);

  return (
    <li>
      <button
        type="button"
        // No-op in v1 — clicking a type doesn't navigate anywhere yet.
        // Title attribute keeps the tooltip helpful.
        title={label}
        className={cn(
          'flex h-7 w-full items-center gap-1.5 rounded-md px-2 text-[13px] text-foreground/85',
          'hover:bg-foreground/5',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <Sparkles className="h-4 w-4 shrink-0 text-foreground/60" aria-hidden />
        <span className="flex-1 truncate text-left">{label}</span>
        {count > 0 && (
          <span className="text-xs tabular-nums text-foreground/40">{count}</span>
        )}
      </button>
    </li>
  );
}

/**
 * Cross-object query filtered to objects whose `any.types` contains
 * the given type id. One useQuery per visible type — limited use
 * today (a handful of types per space), batch later if needed.
 */
function useTypeObjectCount(spaceId: string, typeId: string): number {
  const q = useQuery({
    queryKey: ['objects', spaceId, 'count-by-type', typeId],
    queryFn: ({ signal }) =>
      queryObjects(spaceId, { filter: { 'any.types': typeId } }, signal),
  });
  return q.data?.length ?? 0;
}
