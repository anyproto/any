import { useCallback, type KeyboardEvent, type MouseEvent } from 'react';
import { useAtom, useSetAtom } from 'jotai';
import { useQueryClient } from '@tanstack/react-query';
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  useObjectChildren,
  useMoveObject,
  type ObjectRecord,
  NAV_FOLDER,
  NAV_ROOT_PARENT_ID,
} from '@/lib/api/objects';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui/Toast';
import { nav as lexid } from '@/lib/lexid';
import {
  clearTreeSelectionAtom,
  pendingBulkDeleteAtom,
} from '@/atoms/tree-selection';
import { BulkDeleteObjectsDialog } from '@/components/objects/BulkDeleteObjectsDialog';
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
export function ObjectTree({ spaceId }: { spaceId: string }) {
  const q = useObjectChildren(spaceId, NAV_ROOT_PARENT_ID);
  const move = useMoveObject(spaceId);
  const qc = useQueryClient();
  const clearSelection = useSetAtom(clearTreeSelectionAtom);
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
    [qc, spaceId, move],
  );

  if (q.isPending) {
    return (
      <ul aria-busy="true" className="space-y-1.5 px-2 py-1">
        {[0, 1, 2].map((i) => (
          <li
            key={i}
            aria-hidden
            className="h-5 animate-pulse rounded bg-foreground/10"
          />
        ))}
      </ul>
    );
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
            <ObjectRow key={obj.id} spaceId={spaceId} obj={obj} depth={0} />
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
    queryKey: ['objects', spaceId, 'children'],
  });
  for (const [, list] of all) {
    if (!list) continue;
    const hit = list.find((r) => r.id === objectId);
    if (hit) return hit;
  }
  return null;
}
