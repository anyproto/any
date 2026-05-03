import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  ANY_TYPE_ID,
  NAV_FOLDER,
  NAV_ITEM,
  NAV_ROOT_PARENT_ID,
  type ObjectRecord,
} from './model';
import {
  isObjectsByTypeQuery,
  isObjectsByTypeQueryForSpace,
  objectKeys,
  patchObjectNameEverywhere,
} from './cache';
import {
  createObject,
  deleteObject,
  setObjectProperty,
} from './transport';

/**
 * Set a single property on an object and invalidate caches the
 * type-bar / table view share. No optimistic updates — writes are
 * fast and a refetch keeps the UI in sync without rollback paths.
 */
export function useSetObjectProperty(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      objectId,
      typeId,
      patch,
    }: {
      objectId: string;
      typeId: string;
      patch: Record<string, unknown>;
    }) => {
      if (!spaceId) throw new Error('useSetObjectProperty: no active space');
      return setObjectProperty(spaceId, objectId, typeId, patch);
    },
    onSuccess: (_data, { objectId }) => {
      if (!spaceId) return;
      // The single-record cache + every objects-by-type slice need
      // refresh so the new value shows up everywhere.
      void qc.invalidateQueries({
        queryKey: objectKeys.one(spaceId, objectId),
      });
      void qc.invalidateQueries({
        predicate: (q) => {
          const k = q.queryKey;
          return Array.isArray(k) && isObjectsByTypeQueryForSpace(k, spaceId);
        },
      });
    },
  });
}

/**
 * Rename without needing to know the parent — used by the editor
 * title (which doesn't have parent context handy). Invalidates every
 * objects-cache slice for the space so trees, tables and pickers
 * pick up the new name lazily.
 */
export function useRenameObjectAnywhere(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId, name }: { objectId: string; name: string }) => {
      if (!spaceId) throw new Error('useRenameObjectAnywhere: no active space');
      return setObjectProperty(spaceId, objectId, ANY_TYPE_ID, { name });
    },
    onSuccess: (_data, { objectId, name }) => {
      if (!spaceId) return;
      // Patch mounted tree/table/name caches eagerly so navigation
      // chrome stays in sync before lazy refetches complete.
      patchObjectNameEverywhere(qc, spaceId, objectId, name);
      void qc.invalidateQueries({
        predicate: (q) => {
          const k = q.queryKey;
          return Array.isArray(k) && k[0] === 'objects' && k[1] === spaceId;
        },
      });
    },
  });
}

export interface CreateObjectArgs {
  /**
   * The object's hierarchy home. Defaults to root. This is the
   * canonical location of the object; list/type memberships are
   * optional views layered on top.
   */
  parentId?: string;
  /** Optional ordering lexid inside the hierarchy home. */
  pos?: string;
  /** True for folders (nav.type=2), false/undefined for items (nav.type=1). */
  folder?: boolean;
  /**
   * Optional list/type memberships. These do not decide where the
   * object lives; they only make the object appear in list/database
   * views for those type ids.
   */
  typeIds?: string[];
}

export function buildCreateObjectBody({
  parentId = NAV_ROOT_PARENT_ID,
  pos,
  folder,
  typeIds,
}: CreateObjectArgs = {}): Record<string, unknown> {
  const nav: { type: number; parentId: string; pos?: string } = {
    type: folder ? NAV_FOLDER : NAV_ITEM,
    parentId,
  };
  if (pos) nav.pos = pos;

  const body: Record<string, unknown> = { nav };
  if (typeIds && typeIds.length > 0) body['types'] = typeIds;
  return body;
}

/**
 * Create a new object inside a space. Every app-created object gets an
 * explicit `nav` home. `typeIds` are list/database memberships, not a
 * location; the hierarchy remains the object's home even when it
 * appears in many lists.
 */
export function useCreateObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (args: CreateObjectArgs = {}) => {
      if (!spaceId) throw new Error('useCreateObject: no active space');
      return createObject(spaceId, buildCreateObjectBody(args));
    },
    onSuccess: (_res, args) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: objectKeys.childrenOf(spaceId, args?.parentId ?? NAV_ROOT_PARENT_ID),
      });
      for (const typeId of args?.typeIds ?? []) {
        void qc.invalidateQueries({
          predicate: (q) => {
            const k = q.queryKey;
            return Array.isArray(k) && isObjectsByTypeQuery(k, spaceId, typeId);
          },
        });
      }
      void qc.invalidateQueries({ queryKey: objectKeys.countsByTypeRoot(spaceId) });
    },
  });
}

export interface RenameArgs {
  objectId: string;
  parentId: string;
  name: string;
}

/**
 * Rename an object — POST .../properties/:o/base/any with
 * `{patch: {name}}`. Optimistic: patches the row's `any.name` in the
 * children-of-parent cache before the network round-trip; rolls back
 * on error.
 */
export function useRenameObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId, name }: RenameArgs) => {
      if (!spaceId) throw new Error('useRenameObject: no active space');
      return setObjectProperty(spaceId, objectId, ANY_TYPE_ID, { name });
    },
    onMutate: async ({ objectId, parentId, name }) => {
      if (!spaceId) return;
      const key = objectKeys.childrenOf(spaceId, parentId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<ObjectRecord[]>(key);
      if (previous) {
        qc.setQueryData<ObjectRecord[]>(
          key,
          previous.map((r) =>
            r.id === objectId ? { ...r, any: { ...(r.any ?? {}), name } } : r,
          ),
        );
      }
      return { previous, key };
    },
    onError: (_err, _args, ctx) => {
      if (ctx?.previous && ctx.key) {
        qc.setQueryData(ctx.key, ctx.previous);
      }
    },
    onSuccess: (_data, args) => {
      if (!spaceId) return;
      patchObjectNameEverywhere(qc, spaceId, args.objectId, args.name);
    },
    onSettled: (_data, _err, args) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: objectKeys.childrenOf(spaceId, args.parentId),
      });
    },
  });
}

export interface MoveArgs {
  objectId: string;
  fromParentId: string;
  toParentId: string;
  /** New nav.pos string (computed via lexid client-side). */
  pos: string;
}

/**
 * Move an object to a new parent + position.
 *
 * Sends `POST .../properties/:o/base/nav` with `{patch: {parentId, pos}}`
 * — the server commits both fields in one DAG change.
 *
 * Optimistic: removes the row from the source parent's cached
 * children and inserts it into the target's, sorted by nav.pos.
 * Rolls back on error.
 */
export function useMoveObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId, toParentId, pos }: MoveArgs) => {
      if (!spaceId) throw new Error('useMoveObject: no active space');
      return setObjectProperty(spaceId, objectId, 'nav', { parentId: toParentId, pos });
    },
    onMutate: async ({ objectId, fromParentId, toParentId, pos }) => {
      if (!spaceId) return;
      const fromKey = objectKeys.childrenOf(spaceId, fromParentId);
      const toKey = objectKeys.childrenOf(spaceId, toParentId);
      await Promise.all([
        qc.cancelQueries({ queryKey: fromKey }),
        qc.cancelQueries({ queryKey: toKey }),
      ]);
      const fromPrev = qc.getQueryData<ObjectRecord[]>(fromKey);
      const toPrev = qc.getQueryData<ObjectRecord[]>(toKey);

      // Find the row in source parent's cached list.
      const movedRow =
        fromPrev?.find((r) => r.id === objectId) ??
        toPrev?.find((r) => r.id === objectId); // same-parent moves
      if (!movedRow) return { fromPrev, toPrev, fromKey, toKey };

      // Patch the moved row's nav.parentId + nav.pos optimistically.
      const updatedRow: ObjectRecord = {
        ...movedRow,
        nav: { ...(movedRow.nav ?? {}), parentId: toParentId, pos },
      };

      if (fromParentId !== toParentId) {
        if (fromPrev) {
          qc.setQueryData<ObjectRecord[]>(
            fromKey,
            fromPrev.filter((r) => r.id !== objectId),
          );
        }
        if (toPrev !== undefined) {
          const next = [...toPrev.filter((r) => r.id !== objectId), updatedRow];
          next.sort(byNavPos);
          qc.setQueryData<ObjectRecord[]>(toKey, next);
        }
      } else {
        // Same parent — re-sort the existing list with the new pos.
        if (toPrev) {
          const next = toPrev.map((r) => (r.id === objectId ? updatedRow : r));
          next.sort(byNavPos);
          qc.setQueryData<ObjectRecord[]>(toKey, next);
        }
      }

      return { fromPrev, toPrev, fromKey, toKey };
    },
    onError: (_err, _args, ctx) => {
      if (!ctx) return;
      if (ctx.fromPrev !== undefined) qc.setQueryData(ctx.fromKey, ctx.fromPrev);
      if (ctx.toPrev !== undefined) qc.setQueryData(ctx.toKey, ctx.toPrev);
    },
    onSettled: (_data, _err, args) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: objectKeys.childrenOf(spaceId, args.fromParentId),
      });
      if (args.toParentId !== args.fromParentId) {
        void qc.invalidateQueries({
          queryKey: objectKeys.childrenOf(spaceId, args.toParentId),
        });
      }
    },
  });
}

function byNavPos(a: ObjectRecord, b: ObjectRecord): number {
  const ap = a.nav?.pos ?? '';
  const bp = b.nav?.pos ?? '';
  return ap < bp ? -1 : ap > bp ? 1 : 0;
}

export interface DeleteArgs {
  objectId: string;
  parentId: string;
}

export function useDeleteObject(spaceId: string | null) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ objectId }: DeleteArgs) => {
      if (!spaceId) throw new Error('useDeleteObject: no active space');
      await deleteObject(spaceId, objectId);
    },
    onSuccess: (_res, { parentId }) => {
      if (!spaceId) return;
      void qc.invalidateQueries({
        queryKey: objectKeys.childrenOf(spaceId, parentId),
      });
    },
  });
}
