import {
  patchObjectProp,
  readObjectProp,
  type ObjectRecord,
} from '@/lib/api/objects';
import { uiKind, type PropertyDef } from '@/lib/api/types';
import { assertNever } from '@/shared';

export function readTablePropValue(
  row: ObjectRecord,
  typeId: string,
  propId: string,
): unknown {
  return readObjectProp(row, typeId, propId);
}

export function formatListPropertyValue(
  prop: PropertyDef,
  value: unknown,
): string | null {
  const kind = uiKind(prop);
  if (value == null) return null;

  switch (kind) {
    case 'string':
    case 'longtext':
    case 'date':
    case 'url':
    case 'email':
    case 'relation': {
      if (typeof value !== 'string') return null;
      const trimmed = value.trim();
      return trimmed ? truncatePreview(trimmed) : null;
    }
    case 'number':
      return typeof value === 'number' ? String(value) : null;
    case 'boolean':
      return typeof value === 'boolean' ? (value ? 'Yes' : 'No') : null;
    case 'tags':
      if (!Array.isArray(value)) return null;
      {
        const tagsText = value.filter((v) => typeof v === 'string').join(', ');
        return tagsText ? truncatePreview(tagsText) : null;
      }
    case 'array':
    case 'object':
      return truncatePreview(JSON.stringify(value));
    case 'null':
      return null;
    default:
      return assertNever(kind, 'Unhandled property UI kind');
  }
}

export function patchTableRow(
  row: ObjectRecord,
  patchTypeId: string,
  path: string,
  value: unknown,
): ObjectRecord {
  return patchObjectProp(row, patchTypeId, path, value);
}

function truncatePreview(value: string) {
  return value.length > 80 ? `${value.slice(0, 77)}...` : value;
}
