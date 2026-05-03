import { useAtom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';
import { appStorage } from '@/shared';

/**
 * Per-device override for a space's display name + icon. The SDK
 * doesn't expose a way to write these back to the server yet (see
 * docs/specs/PR-018-space-rename-icon.md), so we keep them client
 * side.
 *
 * `name` overrides the server's name when present; an empty string
 * is treated as "no override" so users can wipe the field to fall
 * back to the auto value.
 *
 * `icon` is a single emoji (or short text). Same fallback rule.
 */
export interface SpaceMetaOverride {
  name?: string;
  icon?: string;
}

export type SpaceMetaOverrides = Record<string, SpaceMetaOverride>;

export const spaceMetaOverridesAtom = atomWithStorage<SpaceMetaOverrides>(
  'any.space.meta.v1',
  {},
  appStorage<SpaceMetaOverrides>(),
  { getOnInit: true },
);

/**
 * Read + write the override for one space. Always returns defined
 * fields (`overrideName`, `overrideIcon`) even when nothing is set,
 * so callers can do `overrideName ?? server.name` without TS dance.
 */
export function useSpaceMeta(spaceId: string | null) {
  const [overrides, setOverrides] = useAtom(spaceMetaOverridesAtom);
  const entry = spaceId ? overrides[spaceId] : undefined;

  const set = (next: SpaceMetaOverride) => {
    if (!spaceId) return;
    const cleaned: SpaceMetaOverride = {};
    if (next.name && next.name.trim().length > 0) cleaned.name = next.name.trim();
    if (next.icon && next.icon.trim().length > 0) cleaned.icon = next.icon.trim();

    setOverrides((prev) => {
      const out = { ...prev };
      if (cleaned.name == null && cleaned.icon == null) {
        delete out[spaceId];
      } else {
        out[spaceId] = cleaned;
      }
      return out;
    });
  };

  const clear = () => {
    if (!spaceId) return;
    setOverrides((prev) => {
      if (!(spaceId in prev)) return prev;
      const out = { ...prev };
      delete out[spaceId];
      return out;
    });
  };

  return {
    overrideName: entry?.name,
    overrideIcon: entry?.icon,
    set,
    clear,
  };
}
