import { atom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';

/**
 * Active space — the one whose contents are shown in pane 2.
 *
 * Persisted so reload restores the user's view.
 */
export const activeSpaceIdAtom = atomWithStorage<string | null>(
  'any.selection.activeSpace.v1',
  null,
  undefined,
  { getOnInit: true },
);

/**
 * What's open in pane 3. Discriminated union so an object and a type
 * can never both claim the pane at once.
 */
export type ActiveView =
  | { kind: 'empty' }
  | { kind: 'object'; objectId: string }
  | { kind: 'type-table'; typeId: string };

export const activeViewAtom = atom<ActiveView>({ kind: 'empty' });

/**
 * Back-compat read/write derived atom — every existing call site that
 * read or wrote `activeObjectIdAtom` keeps working unchanged.
 *
 * Reads: returns the object id when view.kind === 'object', else null.
 * Writes: setting null → empty; setting a string → kind: 'object'.
 */
export const activeObjectIdAtom = atom(
  (get): string | null => {
    const v = get(activeViewAtom);
    return v.kind === 'object' ? v.objectId : null;
  },
  (_get, set, value: string | null) => {
    if (value == null) set(activeViewAtom, { kind: 'empty' });
    else set(activeViewAtom, { kind: 'object', objectId: value });
  },
);

/**
 * Read/write derived atom for the type-table side. Mirror shape.
 */
export const activeTypeIdAtom = atom(
  (get): string | null => {
    const v = get(activeViewAtom);
    return v.kind === 'type-table' ? v.typeId : null;
  },
  (_get, set, value: string | null) => {
    if (value == null) set(activeViewAtom, { kind: 'empty' });
    else set(activeViewAtom, { kind: 'type-table', typeId: value });
  },
);
