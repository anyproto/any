import { describe, it, expect, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useSpaceMeta, spaceMetaOverridesAtom } from './space-meta';
import { getDefaultStore } from 'jotai';

describe('useSpaceMeta', () => {
  beforeEach(() => {
    // Wipe the persisted atom + the localStorage backing.
    getDefaultStore().set(spaceMetaOverridesAtom, {});
    localStorage.clear();
  });

  it('returns undefined fields when no override is set', () => {
    const { result } = renderHook(() => useSpaceMeta('spc-a'));
    expect(result.current.overrideName).toBeUndefined();
    expect(result.current.overrideIcon).toBeUndefined();
  });

  it('set persists trimmed name + icon', () => {
    const { result } = renderHook(() => useSpaceMeta('spc-a'));
    act(() => result.current.set({ name: '  Cooking  ', icon: '🍳' }));
    expect(result.current.overrideName).toBe('Cooking');
    expect(result.current.overrideIcon).toBe('🍳');
  });

  it('empty fields wipe the entry instead of writing empty strings', () => {
    const { result } = renderHook(() => useSpaceMeta('spc-a'));
    act(() => result.current.set({ name: 'Lasagna', icon: '🍝' }));
    expect(result.current.overrideName).toBe('Lasagna');
    act(() => result.current.set({ name: '', icon: '' }));
    expect(result.current.overrideName).toBeUndefined();
    expect(result.current.overrideIcon).toBeUndefined();
  });

  it('clear() removes the entry', () => {
    const { result } = renderHook(() => useSpaceMeta('spc-a'));
    act(() => result.current.set({ name: 'X' }));
    act(() => result.current.clear());
    expect(result.current.overrideName).toBeUndefined();
  });

  it('one space override does not affect another', () => {
    const a = renderHook(() => useSpaceMeta('spc-a'));
    const b = renderHook(() => useSpaceMeta('spc-b'));
    act(() => a.result.current.set({ name: 'A', icon: '📚' }));
    expect(b.result.current.overrideName).toBeUndefined();
  });
});
