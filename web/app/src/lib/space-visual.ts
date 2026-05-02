/**
 * Visual derivations for spaces while we don't have real avatars.
 *
 * `tone(id)` picks one of the five semantic colors deterministically
 * from the space id, so the same space always gets the same tone and
 * different spaces tend to differ.
 *
 * `glyph(name, id)` picks the first letter of the name (uppercased)
 * or "·" if there is no name. Falls back to the first letter of the
 * id so newly-created unnamed spaces still have *something*.
 */
export type SpaceTone = 'accent' | 'success' | 'info' | 'destructive' | 'foreground';

const TONES: readonly SpaceTone[] = ['accent', 'success', 'info', 'destructive', 'foreground'];

// Tiny FNV-1a 32-bit hash. Stable across runs, no external dep.
function fnv1a(str: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < str.length; i++) {
    hash ^= str.charCodeAt(i);
    hash = (hash + ((hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24))) >>> 0;
  }
  return hash >>> 0;
}

export function tone(id: string): SpaceTone {
  return TONES[fnv1a(id) % TONES.length] ?? 'foreground';
}

export function glyph(name: string | undefined, id: string): string {
  const source = (name?.trim() || id).replace(/^[a-z]+_/i, ''); // strip "spc_" prefix etc
  const first = source.charAt(0);
  return first ? first.toUpperCase() : '·';
}
