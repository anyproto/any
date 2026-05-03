import { keyOf } from '@/shared';
import { atom } from 'jotai';

const MAX_OBJECT_TITLE_DRAFTS = 100;

export function objectTitleDraftKey(spaceId: string, objectId: string) {
  return keyOf(spaceId, objectId);
}

/**
 * Live, unsaved object title drafts keyed by space + object. The
 * editor title owns writes; sidebar/header rows read this so typing
 * updates navigation chrome immediately without waiting for blur or
 * the server round trip.
 */
export const objectTitleDraftsAtom = atom<Record<string, string>>({});

export const setObjectTitleDraftAtom = atom(
  null,
  (
    get,
    set,
    {
      spaceId,
      objectId,
      title,
    }: {
      spaceId: string;
      objectId: string;
      title: string;
    },
  ) => {
    const key = objectTitleDraftKey(spaceId, objectId);
    set(objectTitleDraftsAtom, boundDrafts(get(objectTitleDraftsAtom), key, title));
  },
);

export const clearObjectTitleDraftAtom = atom(
  null,
  (get, set, { spaceId, objectId }: { spaceId: string; objectId: string }) => {
    const key = objectTitleDraftKey(spaceId, objectId);
    const current = get(objectTitleDraftsAtom);
    if (!(key in current)) return;
    const next = { ...current };
    delete next[key];
    set(objectTitleDraftsAtom, next);
  },
);

export const clearObjectTitleDraftsAtom = atom(
  null,
  (
    get,
    set,
    { spaceId, objectIds }: { spaceId: string; objectIds: readonly string[] },
  ) => {
    const current = get(objectTitleDraftsAtom);
    let next: Record<string, string> | null = null;
    for (const objectId of objectIds) {
      const key = objectTitleDraftKey(spaceId, objectId);
      if (!(key in current)) continue;
      next ??= { ...current };
      delete next[key];
    }
    if (next) set(objectTitleDraftsAtom, next);
  },
);

function boundDrafts(
  current: Record<string, string>,
  key: string,
  title: string,
): Record<string, string> {
  const next = { ...current };
  delete next[key];
  next[key] = title;

  const keys = Object.keys(next);
  const overflow = keys.length - MAX_OBJECT_TITLE_DRAFTS;
  for (let index = 0; index < overflow; index += 1) {
    delete next[keys[index]!];
  }
  return next;
}
