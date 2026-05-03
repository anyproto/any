import {
  normalizeTableColumnWidth,
  TABLE_NAME_COLUMN_WIDTH,
  TABLE_PROPERTY_COLUMN_WIDTH,
} from '@/atoms';
import type { ObjectRecord } from '@/lib/api/objects';
import type { PropertyDef } from '@/lib/api/types';
import {
  formatListPropertyValue,
  readTablePropValue,
} from './tableObjectValues';

const HEADER_FONT =
  '600 12px Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
const CELL_FONT =
  '400 16px Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';

let measureContext: CanvasRenderingContext2D | null | undefined;

export function autoFitNameColumnWidth(rows: ObjectRecord[]) {
  return autoFitWidth({
    fallback: TABLE_NAME_COLUMN_WIDTH,
    headerLabel: 'Name',
    values: rows.map((row) => row.any?.name?.trim() || 'Untitled'),
    extraWidth: 72,
  });
}

export function autoFitPropertyColumnWidth({
  rows,
  typeId,
  prop,
}: {
  rows: ObjectRecord[];
  typeId: string;
  prop: PropertyDef;
}) {
  return autoFitWidth({
    fallback: TABLE_PROPERTY_COLUMN_WIDTH,
    headerLabel: prop.name ?? `Untitled (${prop.id.slice(0, 6)}...)`,
    values: rows.map((row) => {
      const formatted = formatListPropertyValue(
        prop,
        readTablePropValue(row, typeId, prop.id),
      );
      return formatted ?? '';
    }).filter((value) => value.trim() !== ''),
    extraWidth: 58,
  });
}

function autoFitWidth({
  fallback,
  headerLabel,
  values,
  extraWidth,
}: {
  fallback: number;
  headerLabel: string;
  values: string[];
  extraWidth: number;
}) {
  const headerWidth = measureText(headerLabel.toUpperCase(), HEADER_FONT) + extraWidth;
  const cellWidth =
    values.reduce(
      (max, value) => Math.max(max, measureText(value, CELL_FONT)),
      0,
    ) + extraWidth;
  return normalizeTableColumnWidth(Math.max(headerWidth, cellWidth), fallback);
}

function measureText(text: string, font: string) {
  if (typeof document === 'undefined') return text.length * 8;

  if (measureContext === undefined) {
    measureContext = document
      .createElement('canvas')
      .getContext('2d') as CanvasRenderingContext2D | null;
  }

  if (!measureContext) return text.length * 8;
  measureContext.font = font;
  return measureContext.measureText(text).width;
}
