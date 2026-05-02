import { atom } from 'jotai';

/**
 * Which pane currently owns keyboard focus.
 * Updated by the global ⌘1 / ⌘2 / ⌘3 shortcuts and by focusin events.
 *
 * NOTE: this is a hint for visual indication and shortcuts. It does
 * not move browser focus on its own — see useFocusPane() in App.tsx.
 */
export type PaneId = 1 | 2 | 3;

export const focusedPaneAtom = atom<PaneId>(2);
