import { useState, type MouseEvent } from 'react';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { Plus, Trash2 } from 'lucide-react';
import {
  activeObjectIdAtom,
  activeTypeIdAtom,
  isTypeHidden,
  typeMetaOverridesAtom,
  useTypeMeta,
} from '@/atoms';
import { preloadViewModule } from '@/app/viewModules';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
  toast,
} from '@/components/ui';
import {
  CreateTypeDialog,
  DeleteListDialog,
  ListIcon,
  ListIconDialog,
  RenameListDialog,
} from '@/components/types';
import { ApiError } from '@/lib/api/client';
import { useCreateObject, useTypeObjectCounts } from '@/lib/api/objects';
import {
  useTypes,
  visibleUserTypes,
  type TypeInfo,
} from '@/lib/api/types';
import { cn } from '@/lib/cn';
import { SectionHeader } from './SectionHeader';

export function ListsSection({
  spaceId,
  expanded,
  onToggle,
}: {
  spaceId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const typesQuery = useTypes(spaceId);
  const typeMetaOverrides = useAtomValue(typeMetaOverridesAtom);
  const userTypes = visibleUserTypes(typesQuery.data ?? []).filter(
    (t) => !isTypeHidden(t, typeMetaOverrides, spaceId),
  );
  const countsQuery = useTypeObjectCounts(
    spaceId,
    userTypes.map((t) => t.id),
  );
  const [createTypeOpen, setCreateTypeOpen] = useState(false);

  return (
    <>
      <SectionHeader
        label="Lists"
        expanded={expanded}
        onToggle={onToggle}
        action={
          <button
            type="button"
            aria-label="New list"
            title="New list"
            onClick={(e) => {
              e.stopPropagation();
              setCreateTypeOpen(true);
            }}
            className={cn(
              'inline-flex h-5 w-5 items-center justify-center rounded text-foreground/40',
              'hover:bg-foreground/5 hover:text-foreground/70',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <Plus className="h-3 w-3" aria-hidden />
          </button>
        }
      />
      <CreateTypeDialog open={createTypeOpen} onOpenChange={setCreateTypeOpen} />
      {expanded && (
        <ul className="mt-1 px-1">
          {!typesQuery.isPending && userTypes.length === 0 ? (
            <li className="px-3 py-2 text-xs text-foreground/40">
              No lists yet.
            </li>
          ) : (
            userTypes.map((t) => (
              <TypeRow
                key={t.id}
                spaceId={spaceId}
                type={t}
                count={countsQuery.data?.[t.id] ?? 0}
              />
            ))
          )}
        </ul>
      )}
    </>
  );
}

function TypeRow({
  spaceId,
  type,
  count,
}: {
  spaceId: string;
  type: TypeInfo;
  count: number;
}) {
  const { overrideName, overrideIcon, set: setTypeMeta } = useTypeMeta(spaceId, type.id);
  const label =
    overrideName?.trim() ||
    type.name?.trim() ||
    `Untitled (${type.id.slice(0, 6)}…)`;
  const icon = overrideIcon?.trim();
  const [activeTypeId, setActiveTypeId] = useAtom(activeTypeIdAtom);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const createMutation = useCreateObject(spaceId);
  const active = activeTypeId === type.id;
  const [renameOpen, setRenameOpen] = useState(false);
  const [iconOpen, setIconOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const createListObject = async (e: MouseEvent<HTMLButtonElement>) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      const { objectId } = await createMutation.mutateAsync({ typeIds: [type.id] });
      setActiveObjectId(objectId);
      toast.success(`Created ${label}`);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create object';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <li>
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            className={cn(
              'group relative flex h-7 w-full items-center rounded-md text-[13px] text-foreground/85',
              'hover:bg-foreground/5',
              active && 'bg-foreground/8 text-foreground font-medium',
              'focus-within:ring-2 focus-within:ring-accent',
            )}
          >
            <button
              type="button"
              title={label}
              aria-current={active ? 'page' : undefined}
              onFocus={() => preloadViewModule('type-table')}
              onPointerEnter={() => preloadViewModule('type-table')}
              onClick={() => setActiveTypeId(type.id)}
              className={cn(
                'flex h-full min-w-0 flex-1 items-center gap-1.5 rounded-md px-2 pr-8',
                'focus-visible:outline-none',
              )}
            >
              <ListIcon icon={icon} className="h-4 w-4 shrink-0 text-foreground/60" />
              <span className="flex-1 truncate text-left">{label}</span>
            </button>
            <span
              aria-hidden
              className={cn(
                'pointer-events-none absolute right-2 text-xs tabular-nums text-foreground/40',
                'transition-opacity duration-100 group-hover:opacity-0 group-focus-within:opacity-0',
                count > 0 ? 'opacity-100' : 'opacity-0',
              )}
            >
              {count}
            </span>
            <button
              type="button"
              aria-label={`New ${label}`}
              title={`New ${label}`}
              disabled={createMutation.isPending}
              onClick={(e) => void createListObject(e)}
              className={cn(
                'absolute right-1 inline-flex h-5 w-5 items-center justify-center rounded text-foreground/45',
                'opacity-0 transition-opacity duration-100',
                'hover:bg-foreground/8 hover:text-foreground/80',
                'focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                'group-hover:opacity-100 group-focus-within:opacity-100',
                'disabled:pointer-events-none disabled:opacity-40',
              )}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onSelect={() => setRenameOpen(true)}>Rename</ContextMenuItem>
          <ContextMenuItem onSelect={() => setIconOpen(true)}>Change icon</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onSelect={() => setDeleteOpen(true)}
            className="text-destructive focus:text-destructive"
          >
            <Trash2 className="h-3.5 w-3.5" aria-hidden />
            Delete list
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      <RenameListDialog
        open={renameOpen}
        initialName={label}
        onOpenChange={setRenameOpen}
        onRename={(name) => setTypeMeta({ name })}
      />
      <ListIconDialog
        open={iconOpen}
        icon={icon}
        onOpenChange={setIconOpen}
        onSelect={(nextIcon) => setTypeMeta({ icon: nextIcon })}
      />
      <DeleteListDialog
        open={deleteOpen}
        label={label}
        onOpenChange={setDeleteOpen}
        onDelete={() => {
          setTypeMeta({ hidden: true });
          if (active) setActiveTypeId(null);
          toast.success(`Removed “${label}” from this device`);
        }}
      />
    </li>
  );
}
