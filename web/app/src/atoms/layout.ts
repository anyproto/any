import { atomWithStorage } from 'jotai/utils';
import { appStorage } from '@/shared';

/**
 * Pane widths, persisted to localStorage.
 *
 * Stored as percentages (the unit react-resizable-panels uses
 * internally) so they survive window-size changes meaningfully.
 *
 * v3 defaults:
 *   pane 1 and pane 2 start at their compact/minimum useful open
 *   widths. Storage key bumped from v2 because old toggle behavior
 *   could persist splitter-redistributed widths that made the two left
 *   panes open oversized and codependent.
 *
 * Defaults sized for a 1280-wide window:
 *   ~180px / ~220px / rest  →  14% / 17% / 69%
 */
export interface PaneWidths {
  rail: number; // pane 1, %
  contents: number; // pane 2, %
}

// `getOnInit: true` makes the initial `get(atom)` read from localStorage
// synchronously instead of returning the default until the first
// subscription. Matters for our use case: Vite's first paint of the
// PanelGroup uses these widths as defaultSize — without getOnInit,
// the persisted layout would only be applied after a re-render.
export const paneWidthsAtom = atomWithStorage<PaneWidths>(
  'any.layout.widths.v3',
  { rail: 14, contents: 17 },
  appStorage<PaneWidths>(),
  { getOnInit: true },
);

/**
 * Whether pane 1 is fully closed. Kept separate from the persisted full
 * width so closing the spaces rail does not destroy the user's preferred
 * expanded width.
 */
export const spacesRailClosedAtom = atomWithStorage<boolean>(
  'any.layout.spacesRailClosed.v1',
  false,
  appStorage<boolean>(),
  { getOnInit: true },
);

/**
 * Whether pane 2, the current-space object/list sidebar, is fully closed.
 * This is the panel most users experience as the left sidebar once the
 * spaces rail is closed.
 */
export const spaceContentsClosedAtom = atomWithStorage<boolean>(
  'any.layout.spaceContentsClosed.v1',
  false,
  appStorage<boolean>(),
  { getOnInit: true },
);

/**
 * Min/max bounds, expressed in percent for react-resizable-panels.
 * The library prevents drag past these.
 */
export const PANE_BOUNDS = {
  rail: { closed: 0, min: 14, max: 28, default: 14 }, // closed / ~180–360px expanded on 1280
  contents: { closed: 0, min: 17, max: 38, default: 17 }, // closed / ~220–480px expanded on 1280
} as const;

export function normalizeRailWidth(value: number) {
  if (!Number.isFinite(value) || value < PANE_BOUNDS.rail.min) {
    return PANE_BOUNDS.rail.default;
  }
  return Math.min(value, PANE_BOUNDS.rail.max);
}

export function normalizeContentsWidth(value: number) {
  if (!Number.isFinite(value) || value < PANE_BOUNDS.contents.min) {
    return PANE_BOUNDS.contents.default;
  }
  return Math.min(value, PANE_BOUNDS.contents.max);
}
