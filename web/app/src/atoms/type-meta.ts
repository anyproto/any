import { useAtom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';
import { appStorage, keyOf } from '@/shared';
import type { TypeInfo } from '@/lib/api/types';

export interface TypeMetaOverride {
  name?: string;
  icon?: string;
  hidden?: boolean;
}

export type TypeMetaOverrides = Record<string, TypeMetaOverride>;

export const typeMetaOverridesAtom = atomWithStorage<TypeMetaOverrides>(
  'any.type.meta.v1',
  {},
  appStorage<TypeMetaOverrides>(),
  { getOnInit: true },
);

export function typeMetaKey(spaceId: string, typeId: string) {
  return keyOf(spaceId, typeId);
}

function legacyTypeMetaKey(spaceId: string, typeId: string) {
  return `${spaceId}:${typeId}`;
}

function readTypeMetaOverride(
  overrides: TypeMetaOverrides,
  spaceId: string,
  typeId: string,
): TypeMetaOverride | undefined {
  return (
    overrides[typeMetaKey(spaceId, typeId)] ??
    overrides[legacyTypeMetaKey(spaceId, typeId)]
  );
}

export function typeMetaFor(
  overrides: TypeMetaOverrides,
  spaceId: string,
  typeId: string,
): TypeMetaOverride {
  return readTypeMetaOverride(overrides, spaceId, typeId) ?? {};
}

export function typeDisplayName(
  type: TypeInfo,
  overrides: TypeMetaOverrides,
  spaceId: string,
) {
  const overrideName = typeMetaFor(overrides, spaceId, type.id).name?.trim();
  return overrideName || type.name?.trim() || `Untitled (${type.id.slice(0, 6)}...)`;
}

export function typeDisplayIcon(
  type: TypeInfo,
  overrides: TypeMetaOverrides,
  spaceId: string,
) {
  return typeMetaFor(overrides, spaceId, type.id).icon?.trim();
}

export function isTypeHidden(
  type: TypeInfo,
  overrides: TypeMetaOverrides,
  spaceId: string,
) {
  return typeMetaFor(overrides, spaceId, type.id).hidden === true;
}

export function useTypeMeta(spaceId: string | null, typeId: string | null) {
  const [overrides, setOverrides] = useAtom(typeMetaOverridesAtom);
  const key = spaceId && typeId ? typeMetaKey(spaceId, typeId) : null;
  const legacyKey = spaceId && typeId ? legacyTypeMetaKey(spaceId, typeId) : null;
  const entry =
    key && spaceId && typeId ? readTypeMetaOverride(overrides, spaceId, typeId) : undefined;

  const set = (next: TypeMetaOverride) => {
    if (!key) return;
    setOverrides((prev) => {
      const existing = prev[key] ?? (legacyKey ? prev[legacyKey] : undefined) ?? {};
      const cleaned: TypeMetaOverride = {};
      const name = next.name !== undefined ? next.name : existing.name;
      const icon = next.icon !== undefined ? next.icon : existing.icon;
      const hidden = next.hidden !== undefined ? next.hidden : existing.hidden;

      if (name && name.trim().length > 0) cleaned.name = name.trim();
      if (icon && icon.trim().length > 0) cleaned.icon = icon.trim();
      if (hidden === true) cleaned.hidden = true;

      const out = { ...prev };
      if (legacyKey) delete out[legacyKey];
      if (cleaned.name == null && cleaned.icon == null && cleaned.hidden == null) {
        delete out[key];
      } else {
        out[key] = cleaned;
      }
      return out;
    });
  };

  const clear = () => {
    if (!key) return;
    setOverrides((prev) => {
      if (!(key in prev) && (!legacyKey || !(legacyKey in prev))) return prev;
      const out = { ...prev };
      delete out[key];
      if (legacyKey) delete out[legacyKey];
      return out;
    });
  };

  return {
    overrideName: entry?.name,
    overrideIcon: entry?.icon,
    hidden: entry?.hidden === true,
    set,
    clear,
  };
}
