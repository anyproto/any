/**
 * Cross-object record shape returned by /v1/spaces/:s/objects/query.
 * Mirrors what `internal/nav/nav.go` documents:
 *
 *   { id, any: { name?, types? }, nav: { type, parentId, pos } }
 *
 * `nav.type === 1` is item, `2` is folder.
 */
export interface ObjectRecord {
  id: string;
  any?: {
    name?: string;
    types?: string[];
  };
  nav?: {
    type?: number;
    parentId?: string;
    pos?: string;
  };
  // Other type/property data is present but not relied on yet.
  [key: string]: unknown;
}

export const NAV_ITEM = 1;
export const NAV_FOLDER = 2;
export const NAV_ROOT_PARENT_ID = '';
/** typeId for the built-in `any` namespace (name, description, ...). */
export const ANY_TYPE_ID = 'any';

export class ObjectModel {
  constructor(private readonly record: ObjectRecord) {}

  id(): string {
    return this.record.id;
  }

  name(): string {
    return this.record.any?.name ?? '';
  }

  types(): readonly string[] {
    return this.record.any?.types ?? [];
  }

  prop(typeId: string, propId: string): unknown {
    const ns = this.namespace(typeId);
    return ns?.[propId];
  }

  patchProp(typeId: string, propId: string, value: unknown): ObjectRecord {
    return patchObjectProp(this.record, typeId, propId, value);
  }

  private namespace(typeId: string): Record<string, unknown> | undefined {
    const ns = this.record[typeId];
    if (ns && typeof ns === 'object' && !Array.isArray(ns)) {
      return ns as Record<string, unknown>;
    }
    return undefined;
  }
}

export function objectModel(record: ObjectRecord): ObjectModel {
  return new ObjectModel(record);
}

export function readObjectProp(
  record: ObjectRecord,
  typeId: string,
  propId: string,
): unknown {
  return objectModel(record).prop(typeId, propId);
}

export function patchObjectProp(
  record: ObjectRecord,
  typeId: string,
  propId: string,
  value: unknown,
): ObjectRecord {
  const ns = record[typeId];
  const current =
    ns && typeof ns === 'object' && !Array.isArray(ns)
      ? (ns as Record<string, unknown>)
      : {};
  return {
    ...record,
    [typeId]: { ...current, [propId]: value },
  };
}
