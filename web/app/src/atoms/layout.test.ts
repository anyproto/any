import { describe, it, expect, beforeEach } from 'vitest';
import { createStore } from 'jotai';
import {
  paneWidthsAtom,
  PANE_BOUNDS,
  normalizeContentsWidth,
  normalizeRailWidth,
  spaceContentsClosedAtom,
  spacesRailClosedAtom,
} from './layout';

describe('paneWidthsAtom', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('starts at the documented defaults', () => {
    const store = createStore();
    expect(store.get(paneWidthsAtom)).toEqual({ rail: 14, contents: 17 });
  });

  it('writes new values to localStorage', () => {
    const store = createStore();
    store.set(paneWidthsAtom, { rail: 18, contents: 30 });

    const raw = localStorage.getItem('any.layout.widths.v3');
    expect(raw).not.toBeNull();
    expect(JSON.parse(raw!)).toEqual({ rail: 18, contents: 30 });
  });

  it('reads back the same values within a store', () => {
    const store = createStore();
    store.set(paneWidthsAtom, { rail: 9, contents: 27 });
    expect(store.get(paneWidthsAtom)).toEqual({ rail: 9, contents: 27 });
  });

  it('defaults sit inside the bounds', () => {
    expect(PANE_BOUNDS.rail.closed).toBeLessThan(PANE_BOUNDS.rail.min);
    expect(PANE_BOUNDS.rail.default).toBeGreaterThanOrEqual(PANE_BOUNDS.rail.min);
    expect(PANE_BOUNDS.rail.default).toBeLessThanOrEqual(PANE_BOUNDS.rail.max);
    expect(PANE_BOUNDS.contents.closed).toBeLessThan(PANE_BOUNDS.contents.min);
    expect(PANE_BOUNDS.contents.default).toBeGreaterThanOrEqual(PANE_BOUNDS.contents.min);
    expect(PANE_BOUNDS.contents.default).toBeLessThanOrEqual(PANE_BOUNDS.contents.max);
  });

  it('normalizes broken stored pane widths before reopen', () => {
    expect(normalizeRailWidth(0)).toBe(PANE_BOUNDS.rail.default);
    expect(normalizeRailWidth(Number.NaN)).toBe(PANE_BOUNDS.rail.default);
    expect(normalizeRailWidth(99)).toBe(PANE_BOUNDS.rail.max);
    expect(normalizeRailWidth(18)).toBe(18);

    expect(normalizeContentsWidth(0)).toBe(PANE_BOUNDS.contents.default);
    expect(normalizeContentsWidth(Number.NaN)).toBe(PANE_BOUNDS.contents.default);
    expect(normalizeContentsWidth(99)).toBe(PANE_BOUNDS.contents.max);
    expect(normalizeContentsWidth(20)).toBe(20);
  });
});

describe('spaceContentsClosedAtom', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('starts open', () => {
    const store = createStore();
    expect(store.get(spaceContentsClosedAtom)).toBe(false);
  });

  it('persists closed mode separately from pane widths', () => {
    const store = createStore();
    store.set(spaceContentsClosedAtom, true);

    expect(localStorage.getItem('any.layout.widths.v3')).toBeNull();
    expect(JSON.parse(localStorage.getItem('any.layout.spaceContentsClosed.v1')!)).toBe(true);
  });
});

describe('spacesRailClosedAtom', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('starts expanded', () => {
    const store = createStore();
    expect(store.get(spacesRailClosedAtom)).toBe(false);
  });

  it('persists closed mode separately from pane widths', () => {
    const store = createStore();
    store.set(spacesRailClosedAtom, true);

    expect(localStorage.getItem('any.layout.widths.v3')).toBeNull();
    expect(JSON.parse(localStorage.getItem('any.layout.spacesRailClosed.v1')!)).toBe(true);
  });
});
