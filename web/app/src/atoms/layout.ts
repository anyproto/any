import { atomWithStorage } from 'jotai/utils';

/**
 * Pane widths, persisted to localStorage.
 *
 * Stored as percentages (the unit react-resizable-panels uses
 * internally) so they survive window-size changes meaningfully.
 *
 * Defaults sized for a 1280-wide window:
 *   80px / 280px / rest  →  6.25% / 21.9% / 71.85%
 *
 * Bump the storage key if the panel count or order ever changes.
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
  'any.layout.widths.v1',
  { rail: 6.25, contents: 21.9 },
  undefined,
  { getOnInit: true },
);

/**
 * Min/max bounds, expressed in percent for react-resizable-panels.
 * The library prevents drag past these.
 */
export const PANE_BOUNDS = {
  rail: { min: 4.5, max: 16, default: 6.25 }, // ~60–200px on a 1280 viewport
  contents: { min: 17, max: 38, default: 21.9 }, // ~220–480px on a 1280 viewport
} as const;
