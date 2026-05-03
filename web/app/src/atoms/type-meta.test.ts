import { act, renderHook } from '@testing-library/react';
import { getDefaultStore } from 'jotai';
import { beforeEach, describe, expect, it } from 'vitest';
import {
  isTypeHidden,
  typeDisplayIcon,
  typeDisplayName,
  typeMetaKey,
  typeMetaOverridesAtom,
  useTypeMeta,
} from './type-meta';

describe('useTypeMeta', () => {
  beforeEach(() => {
    getDefaultStore().set(typeMetaOverridesAtom, {});
    localStorage.clear();
  });

  it('persists trimmed list name and icon overrides', () => {
    const { result } = renderHook(() => useTypeMeta('spc-a', 't-book'));
    act(() => result.current.set({ name: '  Books  ', icon: '📚' }));
    expect(result.current.overrideName).toBe('Books');
    expect(result.current.overrideIcon).toBe('📚');
  });

  it('keeps hidden state when changing name', () => {
    const { result } = renderHook(() => useTypeMeta('spc-a', 't-book'));
    act(() => result.current.set({ hidden: true }));
    act(() => result.current.set({ name: 'Library' }));
    expect(result.current.hidden).toBe(true);
    expect(result.current.overrideName).toBe('Library');
  });

  it('display helpers apply overrides and hidden state per space/type', () => {
    const store = getDefaultStore();
    store.set(typeMetaOverridesAtom, {
      [typeMetaKey('spc-a', 't-book')]: { name: 'Library', icon: '📚', hidden: true },
    });
    const overrides = store.get(typeMetaOverridesAtom);
    const type = { id: 't-book', name: 'Books' };

    expect(typeDisplayName(type, overrides, 'spc-a')).toBe('Library');
    expect(typeDisplayIcon(type, overrides, 'spc-a')).toBe('📚');
    expect(isTypeHidden(type, overrides, 'spc-a')).toBe(true);
    expect(typeDisplayName(type, overrides, 'spc-b')).toBe('Books');
  });

  it('can read legacy colon-joined keys while writing the collision-safe key', () => {
    const store = getDefaultStore();
    store.set(typeMetaOverridesAtom, {
      'spc-a:t-book': { name: 'Library', icon: '📚', hidden: true },
    });

    const { result } = renderHook(() => useTypeMeta('spc-a', 't-book'));
    expect(result.current.overrideName).toBe('Library');

    act(() => result.current.set({ name: 'Bookshelf' }));
    const overrides = store.get(typeMetaOverridesAtom);
    expect(overrides[typeMetaKey('spc-a', 't-book')]).toMatchObject({
      name: 'Bookshelf',
      icon: '📚',
      hidden: true,
    });
    expect(overrides['spc-a:t-book']).toBeUndefined();
  });
});
