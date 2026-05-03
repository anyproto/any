import { memo, useCallback, useDeferredValue, useMemo, useState } from 'react';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { selectAtom } from 'jotai/utils';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import {
  AlertCircle,
  Pin,
  Plus,
  Search,
  Settings,
} from 'lucide-react';
import {
  activeSpaceIdAtom,
  lastViewBySpacePreviewAtom,
  type ActiveView,
  focusedPaneAtom,
  settingsOpenAtom,
} from '@/atoms';
import { getSpace, spaceKeys, useSpaces, type SpaceInfo } from '@/lib/api/spaces';
import { ApiError } from '@/lib/api/client';
import {
  getType,
  getTypeProperties,
  listTypes,
  mergeTypeInfo,
  typeKeys,
  type TypeInfo,
} from '@/lib/api/types';
import { getObjectMarkdown, markdownKeys } from '@/lib/api/markdown';
import {
  findObjectNameInCache,
  NAV_ROOT_PARENT_ID,
  objectKeys,
  queryObjects,
  type ObjectsPage,
} from '@/lib/api/objects';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
  Input,
} from '@/components/ui';
import {
  CreateSpaceDialog,
  DeleteSpaceDialog,
  EditSpaceDialog,
} from '@/components/spaces';
import { spaceMetaOverridesAtom } from '@/atoms';
import { SpaceAvatar } from './SpaceAvatar';
import { AccountAvatar } from './AccountAvatar';
import { cn } from '@/lib/cn';
import { useVirtualRows } from '@/shared';

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
  const setSettingsOpen = useSetAtom(settingsOpenAtom);
  const [activeId, setActiveId] = useAtom(activeSpaceIdAtom);
  const spacesQuery = useSpaces();
  const previewSpace = useSpacePreview();
  const [createOpen, setCreateOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<SpaceInfo | null>(null);
  const [pendingEdit, setPendingEdit] = useState<SpaceInfo | null>(null);
  const [filter, setFilter] = useState('');
  const deferredFilter = useDeferredValue(filter);

  const selectSpace = useCallback(
    (spaceId: string) => {
      setActiveId(spaceId);
    },
    [setActiveId],
  );
  const editSpace = useCallback((space: SpaceInfo) => setPendingEdit(space), []);
  const deleteSpace = useCallback((space: SpaceInfo) => setPendingDelete(space), []);

  const filteredSpaces = useMemo(() => {
    const q = deferredFilter.trim().toLowerCase();
    const all = spacesQuery.data ?? [];
    if (q === '') return all;
    return all.filter((s) => (s.name ?? '').toLowerCase().includes(q));
  }, [deferredFilter, spacesQuery.data]);
  const virtualRows = useVirtualRows({
    count: filteredSpaces.length,
    rowHeight: 44,
    overscan: 10,
  });
  const shouldVirtualize = filteredSpaces.length > 80;
  const visibleSpaces = shouldVirtualize
    ? filteredSpaces.slice(virtualRows.startIndex, virtualRows.endIndex)
    : filteredSpaces;

  return (
    <nav
      aria-label="Spaces"
      data-pane="1"
      onFocus={() => setFocused(1)}
      className="flex h-full flex-col overflow-hidden bg-foreground/[0.03]"
    >
      {/* Header row — title + new-space button */}
      <div className="flex items-center justify-between gap-2 px-3 pt-3 pb-2">
        <span className="min-w-0 flex-1 truncate text-xs font-semibold uppercase tracking-wide text-foreground/50">
          Spaces
        </span>
        <div className="flex items-center gap-1">
          <button
            type="button"
            aria-label="New space"
            title="New space"
            onClick={() => setCreateOpen(true)}
            className={cn(
              'inline-flex h-7 w-7 items-center justify-center rounded text-foreground/60',
              'hover:bg-foreground/5 hover:text-foreground',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
          </button>
        </div>
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
            placeholder="Filter spaces..."
            className="h-8 pl-7 text-xs"
            aria-label="Filter spaces"
          />
        </div>
      </div>

      {/* List */}
      <div
        ref={virtualRows.scrollRef}
        onScroll={virtualRows.onScroll}
        className="flex-1 overflow-y-auto px-2 pb-3"
      >
        <ul>
          {spacesQuery.isPending && <SkeletonRows />}
          {spacesQuery.isError && <ListError error={spacesQuery.error} />}
          {spacesQuery.isSuccess && filteredSpaces.length === 0 && (
            <li className="px-2 py-3 text-center text-xs text-foreground/40">
              {filter ? 'No matches' : 'No spaces yet'}
            </li>
          )}
          {spacesQuery.isSuccess && shouldVirtualize && virtualRows.paddingTop > 0 && (
            <li aria-hidden style={{ height: virtualRows.paddingTop }} />
          )}
          {spacesQuery.isSuccess &&
            visibleSpaces.map((space) => (
              <li key={space.id} className="h-11">
                <MemoizedSpaceRow
                  space={space}
                  active={space.id === activeId}
                  onSelect={selectSpace}
                  onPreview={previewSpace}
                  onEdit={editSpace}
                  onDelete={deleteSpace}
                />
              </li>
            ))}
          {spacesQuery.isSuccess && shouldVirtualize && virtualRows.paddingBottom > 0 && (
            <li aria-hidden style={{ height: virtualRows.paddingBottom }} />
          )}
        </ul>
      </div>

      {/* Bottom anchor: account + settings */}
      <div className="flex items-center justify-between gap-2 border-t border-foreground/[0.06] px-3 py-2">
        <AccountAvatar />
        <button
          type="button"
          aria-label="Settings"
          onClick={() => setSettingsOpen(true)}
          className={cn(
            'inline-flex h-7 w-7 items-center justify-center rounded text-foreground/50',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
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
      <EditSpaceDialog
        spaceId={pendingEdit?.id ?? null}
        serverName={pendingEdit?.name}
        onClose={() => setPendingEdit(null)}
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

const TABLE_PREVIEW_PAGE_SIZE = 100;

function SpaceRow({
  space,
  active,
  onSelect,
  onPreview,
  onEdit,
  onDelete,
  extras,
}: {
  space: SpaceInfo;
  active: boolean;
  onSelect: (spaceId: string) => void;
  onPreview: (spaceId: string) => void;
  onEdit: (space: SpaceInfo) => void;
  onDelete: (space: SpaceInfo) => void;
  extras?: RowExtras;
}) {
  const metaAtom = useMemo(
    () => selectAtom(spaceMetaOverridesAtom, (overrides) => overrides[space.id]),
    [space.id],
  );
  const meta = useAtomValue(metaAtom);
  const muted = space.status !== 'active';
  const effectiveName = meta?.name ?? space.name;
  const label = effectiveName?.trim() || `Untitled (${space.id.slice(0, 6)}…)`;
  const pinned = extras?.pinned ?? false;
  const unread = extras?.unread ?? 0;

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <button
          type="button"
          aria-label={label}
          aria-current={active ? 'page' : undefined}
          onFocus={() => onPreview(space.id)}
          onPointerEnter={() => onPreview(space.id)}
          onPointerDown={() => onPreview(space.id)}
          onClick={() => onSelect(space.id)}
          className={cn(
            'group flex w-full items-center gap-2.5 rounded-lg px-2 py-1 text-left transition-colors',
            'text-foreground/85 hover:bg-foreground/5',
            active && 'bg-foreground/8 text-foreground font-medium',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <SpaceAvatar
            spaceId={space.id}
            name={effectiveName}
            size="md"
            muted={muted}
            iconOverride={meta?.icon}
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
        <ContextMenuItem onSelect={() => onEdit(space)}>Edit space...</ContextMenuItem>
        <ContextMenuItem onSelect={() => onDelete(space)} className="text-destructive">
          Delete...
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

const MemoizedSpaceRow = memo(SpaceRow);
MemoizedSpaceRow.displayName = 'SpaceRow';

function useSpacePreview() {
  const qc = useQueryClient();
  const lastViewBySpace = useAtomValue(lastViewBySpacePreviewAtom);
  return useCallback(
    (spaceId: string) => {
      const staleTime = 30_000;
      void qc.prefetchQuery({
        queryKey: spaceKeys.one(spaceId),
        queryFn: ({ signal }) => getSpace(spaceId, signal),
        staleTime,
      });
      void qc.prefetchQuery({
        queryKey: typeKeys.all(spaceId),
        queryFn: ({ signal }) => listTypes(spaceId, signal),
        staleTime,
      });
      void qc.prefetchQuery({
        queryKey: objectKeys.childrenOf(spaceId, NAV_ROOT_PARENT_ID),
        queryFn: ({ signal }) =>
          queryObjects(
            spaceId,
            { filter: { 'nav.parentId': NAV_ROOT_PARENT_ID }, sort: ['nav.pos'] },
            signal,
          ),
        staleTime,
      });
      prefetchRestoredView(qc, spaceId, lastViewBySpace[spaceId], staleTime);
    },
    [lastViewBySpace, qc],
  );
}

function prefetchRestoredView(
  qc: QueryClient,
  spaceId: string,
  view: ActiveView | undefined,
  staleTime: number,
) {
  if (!view || view.kind === 'empty') return;
  if (view.kind === 'object') {
    prefetchObjectView(qc, spaceId, view.objectId, staleTime);
    return;
  }
  if (view.kind === 'type-table') {
    prefetchTypeTableView(qc, spaceId, view.typeId, staleTime);
  }
}

function prefetchObjectView(
  qc: QueryClient,
  spaceId: string,
  objectId: string,
  staleTime: number,
) {
  const cachedName = findObjectNameInCache(qc, spaceId, objectId);
  if (cachedName !== undefined) {
    qc.setQueryData(objectKeys.name(spaceId, objectId), cachedName);
  }

  void qc.prefetchQuery({
    queryKey: objectKeys.one(spaceId, objectId),
    queryFn: async ({ signal }) => {
      const records = await queryObjects(spaceId, { filter: { id: objectId }, limit: 1 }, signal);
      const record = records[0] ?? null;
      if (record?.any?.name !== undefined) {
        qc.setQueryData(objectKeys.name(spaceId, objectId), record.any.name);
      }
      return record;
    },
    staleTime,
  });
  void qc.prefetchQuery({
    queryKey: markdownKeys.one(spaceId, objectId),
    queryFn: ({ signal }) => getObjectMarkdown(spaceId, objectId, signal),
    staleTime,
  });
}

function prefetchTypeTableView(
  qc: QueryClient,
  spaceId: string,
  typeId: string,
  staleTime: number,
) {
  const cachedType = qc
    .getQueryData<TypeInfo[]>(typeKeys.all(spaceId))
    ?.find((type) => type.id === typeId);
  void qc.prefetchQuery({
    queryKey: typeKeys.one(spaceId, typeId),
    queryFn: async ({ signal }) => mergeTypeInfo(cachedType, await getType(spaceId, typeId, signal)),
    staleTime,
  });
  void qc.prefetchQuery({
    queryKey: typeKeys.properties(spaceId, typeId),
    queryFn: ({ signal }) => getTypeProperties(spaceId, typeId, signal),
    staleTime,
  });
  void qc.prefetchInfiniteQuery({
    queryKey: objectKeys.byTypePages(
      spaceId,
      typeId,
      'nav.pos',
      TABLE_PREVIEW_PAGE_SIZE,
    ),
    queryFn: async ({ signal, pageParam }): Promise<ObjectsPage> => {
      const offset = typeof pageParam === 'number' ? pageParam : 0;
      const rows = await queryObjects(
        spaceId,
        {
          filter: { 'any.types': typeId },
          sort: ['nav.pos'],
          limit: TABLE_PREVIEW_PAGE_SIZE + 1,
          offset,
        },
        signal,
      );
      const hasMore = rows.length > TABLE_PREVIEW_PAGE_SIZE;
      const page: ObjectsPage = {
        records: hasMore ? rows.slice(0, TABLE_PREVIEW_PAGE_SIZE) : rows,
      };
      if (hasMore) page.nextOffset = offset + TABLE_PREVIEW_PAGE_SIZE;
      return page;
    },
    initialPageParam: 0,
    getNextPageParam: (lastPage: ObjectsPage) => lastPage.nextOffset,
    staleTime,
  });
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
