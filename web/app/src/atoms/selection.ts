import { atom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';

/**
 * Active space — the one whose contents are shown in pane 2.
 *
 * Persisted so reload restores the user's view. Real space ids land
 * in PR #3; mock ids today.
 */
export const activeSpaceIdAtom = atomWithStorage<string | null>(
  'any.selection.activeSpace.v1',
  null,
  undefined,
  { getOnInit: true },
);

/**
 * Active object — the one rendered in pane 3.
 *
 * Not persisted — opens fresh on reload (no point landing on a
 * possibly-stale object id).
 */
export const activeObjectIdAtom = atom<string | null>(null);
