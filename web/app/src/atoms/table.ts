import { atomWithStorage } from 'jotai/utils';
import { appStorage, keyOf } from '@/shared';

export const TABLE_NAME_COLUMN_WIDTH = 360;
export const TABLE_PROPERTY_COLUMN_WIDTH = 220;
export const TABLE_ADD_COLUMN_WIDTH = 56;
export const TABLE_COLUMN_MIN_WIDTH = 56;
export const TABLE_COLUMN_MAX_WIDTH = 720;

export type TableColumnWidths = Record<string, number>;
export type TableViewLayout = 'table' | 'list' | 'gallery';
export type TableViewLayouts = Record<string, TableViewLayout>;

export const tableColumnWidthsAtom = atomWithStorage<TableColumnWidths>(
  'any.tables.columnWidths.v1',
  {},
  appStorage<TableColumnWidths>(),
  { getOnInit: true },
);

export const tableViewLayoutsAtom = atomWithStorage<TableViewLayouts>(
  'any.tables.viewLayouts.v1',
  {},
  appStorage<TableViewLayouts>(),
  { getOnInit: true },
);

export function tableColumnWidthKey(typeId: string, columnId: string) {
  return keyOf(typeId, columnId);
}

export function legacyTableColumnWidthKey(typeId: string, columnId: string) {
  return `${typeId}:${columnId}`;
}

export function normalizeTableColumnWidth(value: number, fallback: number) {
  if (!Number.isFinite(value)) return fallback;
  return Math.min(TABLE_COLUMN_MAX_WIDTH, Math.max(TABLE_COLUMN_MIN_WIDTH, Math.round(value)));
}

export function normalizeTableViewLayout(value: unknown): TableViewLayout {
  return value === 'list' || value === 'gallery' ? value : 'table';
}
