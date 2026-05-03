import { useCallback, useState, type MouseEvent, type ReactNode } from 'react';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import {
  ChevronDown,
  ChevronsDownUp,
  ChevronsUpDown,
  FileText,
  Folder,
  FolderPlus,
  Plus,
  Settings2,
  Trash2,
} from 'lucide-react';
import {
  activeSpaceIdAtom,
  activeObjectIdAtom,
  activeTypeIdAtom,
  focusedPaneAtom,
  sendTreeExpansionSignalAtom,
  isTypeHidden,
  typeDisplayIcon,
  typeDisplayName,
  typeMetaOverridesAtom,
  useSpaceMeta,
  useTypeMeta,
} from '@/atoms';
import { useSpace } from '@/lib/api/spaces';
import {
  useCreateObject,
  useRenameObjectAnywhere,
  useTypeObjectCounts,
} from '@/lib/api/objects';
import {
  useEnsurePagesList,
  visibleUserTypes,
  isDefaultPagesList,
  useTypes,
  type TypeInfo,
} from '@/lib/api/types';
import { ObjectTree } from '@/components/tree';
import { EditSpaceDialog } from '@/components/spaces';
import { CreateFolderDialog } from '@/components/objects';
import {
  Button,
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
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
import { cn } from '@/lib/cn';
import { preloadViewModule } from '@/app/viewModules';
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
  const [typesOpen, setTypesOpen] = useState(true);

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
  const typesQuery = useTypes(spaceId);
  const { createPage, createTypedObject, createFolder, isPending } = useRootObjectActions(spaceId);
  const [createTypeOpen, setCreateTypeOpen] = useState(false);
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const { overrideName, overrideIcon } = useSpaceMeta(spaceId);
  const typeMetaOverrides = useAtomValue(typeMetaOverridesAtom);

  const effectiveName = (overrideName ?? spaceQuery.data?.name)?.trim();
  const name =
    effectiveName ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);
  // Show user types in the dropdown except the auto-managed "Pages"
  // — it's the implicit default for "New page", listing it again
  // would just duplicate that affordance.
  const userTypes = visibleUserTypes(typesQuery.data ?? []).filter(
    (t) => !isDefaultPagesList(t) && !isTypeHidden(t, typeMetaOverrides, spaceId),
  );

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
        <div className="flex items-center gap-1">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="sm"
                variant="ghost"
                disabled={isPending}
                aria-label="New object"
              >
                <Plus className="h-3.5 w-3.5" aria-hidden />
                {isPending ? 'Creating…' : 'New'}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onSelect={() => void createPage()}>
                <FileText className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                New page
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setCreateFolderOpen(true)}>
                <Folder className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                New folder
              </DropdownMenuItem>
              {userTypes.length > 0 && (
                <>
                  <DropdownMenuSeparator />
                  <DropdownMenuLabel>Lists</DropdownMenuLabel>
                  {userTypes.map((t) => {
                    const label = typeDisplayName(t, typeMetaOverrides, spaceId);
                    const icon = typeDisplayIcon(t, typeMetaOverrides, spaceId);
                    return (
                      <DropdownMenuItem
                        key={t.id}
                        onSelect={() => void createTypedObject([t.id], label)}
                      >
                        <ListIcon icon={icon} className="h-3.5 w-3.5 text-foreground/60" />
                        New {label}
                      </DropdownMenuItem>
                    );
                  })}
                </>
              )}
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => setCreateTypeOpen(true)}>
                <Settings2 className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
                Create list…
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      <CreateFolderDialog
        open={createFolderOpen}
        onOpenChange={setCreateFolderOpen}
        isPending={isPending}
        onCreate={async (name) => {
          await createFolder(name);
          setCreateFolderOpen(false);
        }}
      />
      <CreateTypeDialog open={createTypeOpen} onOpenChange={setCreateTypeOpen} />
      <EditSpaceDialog
        spaceId={editOpen ? spaceId : null}
        serverName={spaceQuery.data?.name}
        onClose={() => setEditOpen(false)}
      />
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
  const { overrideName } = useSpaceMeta(spaceId);
  const { createPage, createFolder, isPending } = useRootObjectActions(spaceId);
  const sendTreeExpansionSignal = useSetAtom(sendTreeExpansionSignalAtom);
  const [foldersExpanded, setFoldersExpanded] = useState(false);
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const label =
    (overrideName ?? spaceQuery.data?.name)?.trim() ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);
  const toggleAllFolders = () => {
    const nextExpanded = !foldersExpanded;
    setFoldersExpanded(nextExpanded);
    if (!expanded) onToggle();
    sendTreeExpansionSignal({
      spaceId,
      action: nextExpanded ? 'expand' : 'collapse',
    });
  };
  return (
    <>
      <SectionHeader
        label={label}
        expanded={expanded}
        onToggle={onToggle}
        action={
          <div className="flex items-center gap-0.5 opacity-70 transition-opacity group-hover/section:opacity-100 focus-within:opacity-100">
            <SectionIconButton
              label="New page"
              title="New page"
              disabled={isPending}
              onClick={() => void createPage()}
            >
              <Plus className="h-3 w-3" aria-hidden />
            </SectionIconButton>
            <SectionIconButton
              label="New folder"
              title="New folder"
              disabled={isPending}
              onClick={() => setCreateFolderOpen(true)}
            >
              <FolderPlus className="h-3 w-3" aria-hidden />
            </SectionIconButton>
            <SectionIconButton
              label={foldersExpanded ? 'Collapse all folders' : 'Expand all folders'}
              title={foldersExpanded ? 'Collapse all folders' : 'Expand all folders'}
              onClick={toggleAllFolders}
            >
              {foldersExpanded ? (
                <ChevronsDownUp className="h-3 w-3" aria-hidden />
              ) : (
                <ChevronsUpDown className="h-3 w-3" aria-hidden />
              )}
            </SectionIconButton>
          </div>
        }
      />
      <CreateFolderDialog
        open={createFolderOpen}
        onOpenChange={setCreateFolderOpen}
        isPending={isPending}
        onCreate={async (name) => {
          await createFolder(name);
          setCreateFolderOpen(false);
        }}
      />
      {expanded && <ObjectTree spaceId={spaceId} />}
    </>
  );
}

function useRootObjectActions(spaceId: string) {
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);
  const createMutation = useCreateObject(spaceId);
  const renameMutation = useRenameObjectAnywhere(spaceId);
  const pagesListId = useEnsurePagesList(spaceId);

  const createPage = useCallback(async () => {
    try {
      const opts: { typeIds?: string[] } = {};
      if (pagesListId) opts.typeIds = [pagesListId];
      const { objectId } = await createMutation.mutateAsync(opts);
      setActiveObjectId(objectId);
      toast.success('Created page');
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to create object';
      toast.error(`${code}: ${msg}`);
    }
  }, [createMutation, pagesListId, setActiveObjectId]);

  const createTypedObject = useCallback(
    async (typeIds: string[], label: string) => {
      try {
        const { objectId } = await createMutation.mutateAsync({ typeIds });
        setActiveObjectId(objectId);
        toast.success(`Created ${label}`);
      } catch (err) {
        const code = err instanceof ApiError ? err.code : 'unknown';
        const msg = err instanceof Error ? err.message : 'Failed to create object';
        toast.error(`${code}: ${msg}`);
      }
    },
    [createMutation, setActiveObjectId],
  );

  const createFolder = useCallback(
    async (name: string) => {
      try {
        const { objectId } = await createMutation.mutateAsync({ folder: true });
        await renameMutation.mutateAsync({ objectId, name });
        toast.success(`Created folder “${name}”`);
      } catch (err) {
        const code = err instanceof ApiError ? err.code : 'unknown';
        const msg = err instanceof Error ? err.message : 'Failed to create folder';
        toast.error(`${code}: ${msg}`);
        throw err;
      }
    },
    [createMutation, renameMutation],
  );

  return {
    createPage,
    createTypedObject,
    createFolder,
    isPending: createMutation.isPending || renameMutation.isPending,
  };
}

function SectionIconButton({
  label,
  title,
  disabled,
  onClick,
  children,
}: {
  label: string;
  title: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={title}
      disabled={disabled}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={cn(
        'inline-flex h-5 w-5 items-center justify-center rounded text-foreground/45',
        'hover:bg-foreground/7 hover:text-foreground/75 disabled:cursor-not-allowed disabled:opacity-40',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      {children}
    </button>
  );
}

/**
 * Real `/v1/types` filtered to user types. Counts are batched through
 * one object query and derived client-side from `any.types`.
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

function TypeRow({ spaceId, type, count }: { spaceId: string; type: TypeInfo; count: number }) {
  const { overrideName, overrideIcon, set: setTypeMeta } = useTypeMeta(spaceId, type.id);
  const label = overrideName?.trim() || type.name?.trim() || `Untitled (${type.id.slice(0, 6)}…)`;
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
