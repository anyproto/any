import { atomWithStorage } from 'jotai/utils';

/**
 * Pane widths, persisted to localStorage.
 *
 * Stored as percentages (the unit react-resizable-panels uses
 * internally) so they survive window-size changes meaningfully.
 *
 * v2 defaults (PR #11):
 *   pane 1 widened from a slim icon rail (~80px) to a list (~280px).
 *   pane 2 stays where it was. Storage key bumped so users on v1
 *   don't get an 80px pane 1 inherited into the new design.
 *
 * Defaults sized for a 1280-wide window:
 *   280px / 320px / rest  →  21.9% / 25% / 53.1%
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
  'any.layout.widths.v2',
  { rail: 21.9, contents: 25 },
  undefined,
  { getOnInit: true },
);

/**
 * Min/max bounds, expressed in percent for react-resizable-panels.
 * The library prevents drag past these.
 */
export const PANE_BOUNDS = {
  rail: { min: 14, max: 28, default: 21.9 }, // ~180–360px on a 1280 viewport
  contents: { min: 17, max: 38, default: 25 }, // ~220–480px on a 1280 viewport
} as const;
