import type { PropertyDef } from '@/lib/api/types';

export type SortDir = 'asc' | 'desc' | null;

export const TABLE_PAGE_SIZE = 100;

export function sortLabel(props: PropertyDef[], typeId: string, sortKey: string) {
  if (sortKey === 'nav.pos') return 'Default';
  if (sortKey === 'any.name') return 'Name';
  return props.find((p) => `${typeId}.${p.id}` === sortKey)?.name ?? 'Property';
}

export function sortSummary(
  props: PropertyDef[],
  typeId: string,
  sortKey: string,
  sortDir: SortDir,
) {
  const label = sortLabel(props, typeId, sortKey);
  if (sortKey === 'nav.pos' && sortDir === 'asc') return 'Default';
  return `${label}, ${sortDir === 'desc' ? 'desc' : 'asc'}`;
}
