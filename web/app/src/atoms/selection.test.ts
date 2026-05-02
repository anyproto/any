import { describe, it, expect } from 'vitest';
import { createStore } from 'jotai';
import {
  activeViewAtom,
  activeObjectIdAtom,
  activeTypeIdAtom,
} from './selection';

describe('activeViewAtom', () => {
  it('starts empty', () => {
    const store = createStore();
    expect(store.get(activeViewAtom)).toEqual({ kind: 'empty' });
  });

  it('writing activeObjectIdAtom flips view to object kind', () => {
    const store = createStore();
    store.set(activeObjectIdAtom, 'obj-1');
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'object',
      objectId: 'obj-1',
    });
    expect(store.get(activeObjectIdAtom)).toBe('obj-1');
    expect(store.get(activeTypeIdAtom)).toBeNull();
  });

  it('writing activeTypeIdAtom flips view to type-table', () => {
    const store = createStore();
    store.set(activeTypeIdAtom, 't_recipe');
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'type-table',
      typeId: 't_recipe',
    });
    expect(store.get(activeTypeIdAtom)).toBe('t_recipe');
    expect(store.get(activeObjectIdAtom)).toBeNull();
  });

  it('object → type-table → object cycles cleanly', () => {
    const store = createStore();
    store.set(activeObjectIdAtom, 'obj-1');
    store.set(activeTypeIdAtom, 't_recipe');
    expect(store.get(activeObjectIdAtom)).toBeNull();
    store.set(activeObjectIdAtom, 'obj-2');
    expect(store.get(activeTypeIdAtom)).toBeNull();
  });

  it('null clears the view', () => {
    const store = createStore();
    store.set(activeObjectIdAtom, 'obj-1');
    store.set(activeObjectIdAtom, null);
    expect(store.get(activeViewAtom)).toEqual({ kind: 'empty' });
  });
});
