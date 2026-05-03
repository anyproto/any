import { describe, it, expect } from 'vitest';
import { createStore } from 'jotai';
import {
  activeSpaceIdAtom,
  activeViewAtom,
  activeObjectIdAtom,
  activeTypeIdAtom,
  canGoBackAtom,
  canGoForwardAtom,
  focusedPaneAtom,
  goBackAtom,
  goForwardAtom,
  openObjectFromTypeAtom,
  pendingBulkDeleteAtom,
  renamingObjectIdAtom,
  selectedTreeIdsAtom,
  treeSelectionAnchorAtom,
  treeSelectionKeyboardEdgeAtom,
  viewStateAtom,
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

  it('can open an object with a source type context', () => {
    const store = createStore();
    store.set(openObjectFromTypeAtom, {
      objectId: 'obj-1',
      typeId: 't_movie',
    });

    expect(store.get(activeViewAtom)).toEqual({
      kind: 'object',
      objectId: 'obj-1',
      typeId: 't_movie',
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

  it('tracks back and forward history across pane views', () => {
    const store = createStore();
    store.set(activeSpaceIdAtom, 'spc-history');
    store.set(activeObjectIdAtom, 'obj-1');
    store.set(activeTypeIdAtom, 'type-books');
    store.set(activeObjectIdAtom, 'obj-2');

    expect(store.get(canGoBackAtom)).toBe(true);
    expect(store.get(canGoForwardAtom)).toBe(false);

    store.set(goBackAtom);
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'type-table',
      typeId: 'type-books',
    });
    expect(store.get(canGoForwardAtom)).toBe(true);

    store.set(goBackAtom);
    expect(store.get(activeObjectIdAtom)).toBe('obj-1');

    store.set(goForwardAtom);
    expect(store.get(activeViewAtom)).toEqual({
      kind: 'type-table',
      typeId: 'type-books',
    });
  });

  it('remembers the last pane view for each space', () => {
    const store = createStore();
    store.set(activeSpaceIdAtom, 'spc-remember-a');
    store.set(activeObjectIdAtom, 'obj-a');

    store.set(activeSpaceIdAtom, 'spc-remember-b');
    expect(store.get(activeViewAtom)).toEqual({ kind: 'empty' });
    store.set(activeTypeIdAtom, 'type-b');

    store.set(activeSpaceIdAtom, 'spc-remember-a');
    expect(store.get(activeObjectIdAtom)).toBe('obj-a');

    store.set(activeSpaceIdAtom, 'spc-remember-b');
    expect(store.get(activeTypeIdAtom)).toBe('type-b');
  });

  it('back restores the previous space and its view after a space switch', () => {
    const store = createStore();
    store.set(activeSpaceIdAtom, 'spc-cross-a');
    store.set(activeObjectIdAtom, 'obj-cross-a');

    store.set(activeSpaceIdAtom, 'spc-cross-b');
    expect(store.get(activeSpaceIdAtom)).toBe('spc-cross-b');
    expect(store.get(activeViewAtom)).toEqual({ kind: 'empty' });

    store.set(goBackAtom);
    expect(store.get(activeSpaceIdAtom)).toBe('spc-cross-a');
    expect(store.get(activeObjectIdAtom)).toBe('obj-cross-a');
  });

  it('exposes navigation and transient UI state through viewStateAtom', () => {
    const store = createStore();
    store.set(activeSpaceIdAtom, 'spc-view-state');
    store.set(activeTypeIdAtom, 'type-view-state');
    store.set(focusedPaneAtom, 3);
    store.set(selectedTreeIdsAtom, new Set(['obj-a', 'obj-b']));
    store.set(treeSelectionAnchorAtom, { id: 'obj-a', parentId: 'root' });
    store.set(treeSelectionKeyboardEdgeAtom, 'obj-b');
    store.set(renamingObjectIdAtom, 'obj-a');
    store.set(pendingBulkDeleteAtom, ['obj-a', 'obj-b']);

    const state = store.get(viewStateAtom);
    expect(state.activeSpaceId).toBe('spc-view-state');
    expect(state.activeView).toEqual({
      kind: 'type-table',
      typeId: 'type-view-state',
    });
    expect(state.focusedPane).toBe(3);
    expect([...state.treeSelection.ids]).toEqual(['obj-a', 'obj-b']);
    expect(state.treeSelection.anchor).toEqual({ id: 'obj-a', parentId: 'root' });
    expect(state.treeSelection.keyboardEdgeId).toBe('obj-b');
    expect(state.renamingObjectId).toBe('obj-a');
    expect(state.pendingBulkDelete).toEqual(['obj-a', 'obj-b']);
  });
});
