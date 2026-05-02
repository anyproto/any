import { describe, it, expect, beforeEach } from 'vitest';
import { createStore } from 'jotai';
import { paneWidthsAtom, PANE_BOUNDS } from './layout';

describe('paneWidthsAtom', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('starts at the documented defaults', () => {
    const store = createStore();
    expect(store.get(paneWidthsAtom)).toEqual({ rail: 6.25, contents: 21.9 });
  });

  it('writes new values to localStorage', () => {
    const store = createStore();
    store.set(paneWidthsAtom, { rail: 8, contents: 25 });

    const raw = localStorage.getItem('any.layout.widths.v1');
    expect(raw).not.toBeNull();
    expect(JSON.parse(raw!)).toEqual({ rail: 8, contents: 25 });
  });

  it('reads back the same values within a store', () => {
    const store = createStore();
    store.set(paneWidthsAtom, { rail: 9, contents: 27 });
    expect(store.get(paneWidthsAtom)).toEqual({ rail: 9, contents: 27 });
  });

  it('defaults sit inside the bounds', () => {
    expect(PANE_BOUNDS.rail.default).toBeGreaterThanOrEqual(PANE_BOUNDS.rail.min);
    expect(PANE_BOUNDS.rail.default).toBeLessThanOrEqual(PANE_BOUNDS.rail.max);
    expect(PANE_BOUNDS.contents.default).toBeGreaterThanOrEqual(PANE_BOUNDS.contents.min);
    expect(PANE_BOUNDS.contents.default).toBeLessThanOrEqual(PANE_BOUNDS.contents.max);
  });
});
