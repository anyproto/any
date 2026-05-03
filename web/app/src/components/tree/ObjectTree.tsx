import { useCallback, type KeyboardEvent, type MouseEvent } from 'react';
import { useAtom, useSetAtom, useStore } from 'jotai';
import { useQueryClient } from '@tanstack/react-query';
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  objectKeys,
  useObjectChildren,
  useMoveObject,
  type ObjectRecord,
  NAV_FOLDER,
  NAV_ROOT_PARENT_ID,
} from '@/lib/api/objects';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui';
import { nav as lexid } from '@/lib/lexid';
import {
  clearTreeSelectionAtom,
  pendingBulkDeleteAtom,
  selectedTreeIdsAtom,
} from '@/atoms';
import { BulkDeleteObjectsDialog } from '@/components/objects';
import { ObjectRow } from './ObjectRow';

/**
 * The root of the object tree for the active space.
 *
 * Owns the DndContext and the onDragEnd reducer that turns a drop
 * into a `useMoveObject` mutation. Drop semantics:
 *   - Drop on a folder row → moves under that folder, last child.
 *   - Drop on an item row  → moves to the same parent as that item,
 *     positioned right after it.
 *
 * Self-drop and same-position drops are no-ops.
 */
export function ObjectTree({
  spaceId,
  pagesListId,
}: {
  spaceId: string;
  pagesListId?: string | null | undefined;
}) {
  const q = useObjectChildren(spaceId, NAV_ROOT_PARENT_ID);
  const move = useMoveObject(spaceId);
  const qc = useQueryClient();
  const clearSelection = useSetAtom(clearTreeSelectionAtom);
  const store = useStore();
  const [pendingBulk, setPendingBulk] = useAtom(pendingBulkDeleteAtom);
  // Pointer sensor with a short activation distance so the row's
  // built-in click (select) still fires on a plain click.
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));

  // Esc clears multi-selection.
  const onTreeKeyDown = useCallback(
    (e: KeyboardEvent<HTMLDivElement>) => {
      if (e.key === 'Escape') {
        clearSelection();
      }
    },
    [clearSelection],
  );

  // Click directly on the wrapping container (not a row) clears the
  // multi-selection — Finder behaviour.
  const onWrapperClick = useCallback(
    (e: MouseEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) clearSelection();
    },
    [clearSelection],
  );

  const onDragEnd = useCallback(
    (e: DragEndEvent) => {
      const sourceId = String(e.active.id);
      const targetId = e.over?.id != null ? String(e.over.id) : null;
      if (!targetId || targetId === sourceId) return;

      // Find both objects via cached queries. The active row is in
      // exactly one children-of-parent cache (its current parent);
      // we walk known caches to find it.
      const source = findObjectInCache(qc, spaceId, sourceId);
      const target = findObjectInCache(qc, spaceId, targetId);
      if (!source || !target) return;

      const selectedIds = store.get(selectedTreeIdsAtom);
      const batchIds =
        selectedIds.size > 1 && selectedIds.has(sourceId)
          ? Array.from(selectedIds)
          : [];
      if (batchIds.length > 1) {
        if (target.nav?.type !== NAV_FOLDER) {
          toast.error('Drop selected items on a folder to move them together.');
          return;
        }
        if (batchIds.includes(targetId)) {
          toast.error('Cannot move selected items into themselves.');
          return;
        }
        const moves = buildBatchFolderMoves(qc, spaceId, batchIds, targetId);
        if (moves.length === 0) return;
        void (async () => {
          for (const nextMove of moves) {
            await move.mutateAsync(nextMove);
          }
          clearSelection();
          toast.success(`Moved ${moves.length} items`);
        })().catch((err: unknown) => {
          const code = err instanceof ApiError ? err.code : 'unknown';
          const msg = err instanceof Error ? err.message : 'Move failed';
          toast.error(`${code}: ${msg}`);
        });
        return;
      }

      const sourceParentId = source.nav?.parentId ?? NAV_ROOT_PARENT_ID;
      const isFolderTarget = target.nav?.type === NAV_FOLDER;

      let toParentId: string;
      let pos: string;
      try {
        if (isFolderTarget) {
          toParentId = target.id;
          const children = qc.getQueryData<ObjectRecord[]>([
            'objects',
            spaceId,
            'children',
            toParentId,
          ]);
          const maxPos = children?.reduce<string>(
            (acc, r) => ((r.nav?.pos ?? '') > acc ? (r.nav?.pos ?? '') : acc),
            '',
          ) ?? '';
          pos = maxPos === '' ? lexid.middle() : lexid.next(maxPos);
        } else {
          toParentId = target.nav?.parentId ?? NAV_ROOT_PARENT_ID;
          const siblings = qc.getQueryData<ObjectRecord[]>([
            'objects',
            spaceId,
            'children',
            toParentId,
          ]) ?? [];
          // Find target's index, then the next sibling (if any).
          const sortedSiblings = [...siblings].sort((a, b) =>
            (a.nav?.pos ?? '') < (b.nav?.pos ?? '') ? -1 : 1,
          );
          const idx = sortedSiblings.findIndex((r) => r.id === target.id);
          const targetPos = target.nav?.pos ?? '';
          // Skip the row being moved when picking the next sibling.
          let nextSiblingPos = '';
          for (let i = idx + 1; i < sortedSiblings.length; i++) {
            const cand = sortedSiblings[i]!;
            if (cand.id === sourceId) continue;
            nextSiblingPos = cand.nav?.pos ?? '';
            break;
          }
          if (nextSiblingPos === '') {
            pos = lexid.next(targetPos);
          } else {
            pos = lexid.nextBefore(targetPos, nextSiblingPos);
          }
        }
      } catch (err) {
        const code = err instanceof ApiError ? err.code : 'lexid';
        toast.error(`${code}: ${err instanceof Error ? err.message : String(err)}`);
        return;
      }

      // Skip if nothing changed.
      if (toParentId === sourceParentId && pos === source.nav?.pos) return;

      void move
        .mutateAsync({
          objectId: sourceId,
          fromParentId: sourceParentId,
          toParentId,
          pos,
        })
        .catch((err: unknown) => {
          const code = err instanceof ApiError ? err.code : 'unknown';
          const msg = err instanceof Error ? err.message : 'Move failed';
          toast.error(`${code}: ${msg}`);
        });
    },
    [clearSelection, qc, spaceId, move, store],
  );

  if (q.isPending) {
    return <div aria-busy="true" />;
  }

  if (q.isError) {
    const code = q.error instanceof ApiError ? q.error.code : 'unknown';
    const message = q.error instanceof Error ? q.error.message : 'Failed to load objects';
    return (
      <div
        role="alert"
        className="m-2 rounded-md border border-destructive/30 bg-destructive/[0.06] p-3 text-xs text-foreground"
      >
        <p className="font-medium text-destructive">Couldn&rsquo;t load objects</p>
        <p className="mt-1 text-foreground/70">
          <code className="font-mono">{code}</code> — {message}
        </p>
      </div>
    );
  }

  if (!q.data || q.data.length === 0) {
    return (
      <div className="px-3 py-6 text-center text-xs text-foreground/50">
        No objects yet —
        <br />
        create one with <span className="font-medium text-foreground/80">+ New</span>.
      </div>
    );
  }

  return (
    <DndContext sensors={sensors} onDragEnd={onDragEnd}>
      {/*
        Wrapper exists purely to capture clicks on the empty area
        below the last row + Esc keypress within the tree. It is
        not itself an interactive widget, so a11y role doesn't
        apply — both gestures are convenience clears that mirror
        Finder's "click empty space deselects" behaviour.
       */}
      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions */}
      <div
        onKeyDown={onTreeKeyDown}
        onClick={onWrapperClick}
        className="min-h-full"
      >
        <ul role="tree" aria-label="Objects" className="px-1 py-1">
          {q.data.map((obj) => (
            <ObjectRow
              key={obj.id}
              spaceId={spaceId}
              obj={obj}
              parentId={NAV_ROOT_PARENT_ID}
              depth={0}
              pagesListId={pagesListId}
            />
          ))}
        </ul>
      </div>
      <BulkDeleteObjectsDialog
        spaceId={spaceId}
        ids={pendingBulk}
        onClose={() => setPendingBulk(null)}
      />
    </DndContext>
  );
}

export function buildBatchFolderMoves(
  qc: ReturnType<typeof useQueryClient>,
  spaceId: string,
  selectedIds: string[],
  targetFolderId: string,
) {
  const targetChildren = qc.getQueryData<ObjectRecord[]>([
    'objects',
    spaceId,
    'children',
    targetFolderId,
  ]);
  let tailPos =
    targetChildren?.reduce<string>(
      (acc, r) => ((r.nav?.pos ?? '') > acc ? (r.nav?.pos ?? '') : acc),
      '',
    ) ?? '';

  const selectedObjects = selectedIds
    .map((id) => findObjectInCache(qc, spaceId, id))
    .filter((obj): obj is ObjectRecord => obj != null)
    .sort(compareTreeRecords);

  const moves = [];
  for (const obj of selectedObjects) {
    if (obj.id === targetFolderId) continue;
    const fromParentId = obj.nav?.parentId ?? NAV_ROOT_PARENT_ID;
    tailPos = tailPos === '' ? lexid.middle() : lexid.next(tailPos);
    moves.push({
      objectId: obj.id,
      fromParentId,
      toParentId: targetFolderId,
      pos: tailPos,
    });
  }
  return moves;
}

export function compareTreeRecords(a: ObjectRecord, b: ObjectRecord) {
  const parentA = a.nav?.parentId ?? NAV_ROOT_PARENT_ID;
  const parentB = b.nav?.parentId ?? NAV_ROOT_PARENT_ID;
  if (parentA !== parentB) return parentA < parentB ? -1 : 1;
  const posA = a.nav?.pos ?? '';
  const posB = b.nav?.pos ?? '';
  if (posA !== posB) return posA < posB ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

/**
 * Walk the children-of-parent caches looking for an object by id.
 * Returns the cached row or null.
 *
 * Cheap: limited to caches we already populated. Doesn't fetch.
 */
function findObjectInCache(
  qc: ReturnType<typeof useQueryClient>,
  spaceId: string,
  objectId: string,
): ObjectRecord | null {
  const all = qc.getQueriesData<ObjectRecord[]>({
    queryKey: objectKeys.childrenRoot(spaceId),
  });
  for (const [, list] of all) {
    if (!list) continue;
    const hit = list.find((r) => r.id === objectId);
    if (hit) return hit;
  }
  return null;
}
