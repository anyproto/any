import { atom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';

/**
 * Theme preference. Three values:
 *   'system' — follow prefers-color-scheme (default for first-run)
 *   'light'  — explicit light mode
 *   'dark'   — explicit dark mode
 *
 * Persisted in localStorage under `any.theme`.
 */
export type ThemePreference = 'system' | 'light' | 'dark';

export const themePreferenceAtom = atomWithStorage<ThemePreference>(
  'any.theme',
  'system',
  undefined,
  { getOnInit: true },
);

/**
 * The actual mode applied to <html data-theme="…">. Resolves 'system'
 * to a concrete light|dark by reading prefers-color-scheme.
 *
 * NOTE: this atom is read-only; it tracks the OS preference live via
 * the matchMedia listener installed by `installThemeEffect`.
 */
const systemPrefersDarkAtom = atom<boolean>(
  typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-color-scheme: dark)').matches === true,
);

export const resolvedThemeAtom = atom<'light' | 'dark'>((get) => {
  const pref = get(themePreferenceAtom);
  if (pref === 'system') {
    return get(systemPrefersDarkAtom) ? 'dark' : 'light';
  }
  return pref;
});

/**
 * Wire up media-query and DOM side-effects. Call once at app startup
 * with the Jotai store; returns a cleanup function.
 *
 * Kept as a free function (not a hook) so the storybook preview can
 * reuse it without React state plumbing.
 */
export function installThemeEffect(store: {
  get: <T>(atom: import('jotai').Atom<T>) => T;
  set: <T>(atom: import('jotai').WritableAtom<T, [T], unknown>, value: T) => void;
  sub: (atom: import('jotai').Atom<unknown>, cb: () => void) => () => void;
}): () => void {
  if (typeof window === 'undefined') return () => undefined;

  const mql = window.matchMedia('(prefers-color-scheme: dark)');
  const onMql = () => {
    store.set(systemPrefersDarkAtom, mql.matches);
  };
  mql.addEventListener('change', onMql);

  const apply = () => {
    const mode = store.get(resolvedThemeAtom);
    document.documentElement.dataset['theme'] = mode;
  };
  apply();
  const unsub = store.sub(resolvedThemeAtom, apply);

  return () => {
    mql.removeEventListener('change', onMql);
    unsub();
  };
}
