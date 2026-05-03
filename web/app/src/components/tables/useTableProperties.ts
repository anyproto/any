import { useCallback, useEffect, useMemo, useState } from 'react';
import { useAtom } from 'jotai';
import {
  legacyTableColumnWidthKey,
  normalizeTableColumnWidth,
  tableColumnWidthKey,
  tableColumnWidthsAtom,
  TABLE_ADD_COLUMN_WIDTH,
  TABLE_NAME_COLUMN_WIDTH,
  TABLE_PROPERTY_COLUMN_WIDTH,
} from '@/atoms';
import {
  useTypeProperties,
  type PropertyDef,
  type PropertyKind,
} from '@/lib/api/types';

export function useTableProperties(spaceId: string | null, typeId: string) {
  const propsQuery = useTypeProperties(spaceId, typeId);
  const [hiddenPropIds, setHiddenPropIds] = useState<Set<string>>(() => new Set());
  const [draggedColumnPropId, setDraggedColumnPropId] = useState<string | null>(null);
  const [columnDropTargetPropId, setColumnDropTargetPropId] = useState<string | null>(
    null,
  );
  const [columnWidths, setColumnWidths] = useAtom(tableColumnWidthsAtom);

  const rawUserProps = useMemo(
    () => (propsQuery.data ?? []).filter((p) => isUserKind(p.kind)),
    [propsQuery.data],
  );
  const rawUserPropIds = useMemo(() => rawUserProps.map((p) => p.id), [rawUserProps]);
  const rawUserPropIdsKey = rawUserPropIds.join('\u0000');
  const [propertyOrder, setPropertyOrder] = useState<string[]>([]);

  useEffect(() => {
    setPropertyOrder((prev) => {
      const next = prev.filter((id) => rawUserPropIds.includes(id));
      for (const id of rawUserPropIds) {
        if (!next.includes(id)) next.push(id);
      }
      if (next.length === prev.length && next.every((id, index) => id === prev[index]))
        return prev;
      return next;
    });
  }, [rawUserPropIds, rawUserPropIdsKey]);

  const userProps = useMemo(() => {
    if (propertyOrder.length === 0) return rawUserProps;
    const byId = new Map(rawUserProps.map((p) => [p.id, p]));
    const ordered = propertyOrder
      .map((id) => byId.get(id))
      .filter((p): p is PropertyDef => p != null);
    const orderedIds = new Set(ordered.map((p) => p.id));
    return [...ordered, ...rawUserProps.filter((p) => !orderedIds.has(p.id))];
  }, [propertyOrder, rawUserProps]);

  const visibleProps = useMemo(
    () => userProps.filter((p) => !hiddenPropIds.has(p.id)),
    [hiddenPropIds, userProps],
  );

  const columnWidth = useCallback(
    (columnId: string, fallback: number) => {
      const width =
        columnWidths[tableColumnWidthKey(typeId, columnId)] ??
        columnWidths[legacyTableColumnWidthKey(typeId, columnId)] ??
        fallback;
      return normalizeTableColumnWidth(width, fallback);
    },
    [columnWidths, typeId],
  );
  const nameColumnWidth = columnWidth('name', TABLE_NAME_COLUMN_WIDTH);
  const propertyColumnWidths = useMemo(() => {
    const widths: Record<string, number> = {};
    for (const prop of visibleProps) {
      widths[prop.id] = columnWidth(prop.id, TABLE_PROPERTY_COLUMN_WIDTH);
    }
    return widths;
  }, [columnWidth, visibleProps]);
  const tablePixelWidth = useMemo(
    () =>
      nameColumnWidth +
      visibleProps.reduce(
        (sum, prop) => sum + (propertyColumnWidths[prop.id] ?? TABLE_PROPERTY_COLUMN_WIDTH),
        0,
      ) +
      TABLE_ADD_COLUMN_WIDTH,
    [nameColumnWidth, propertyColumnWidths, visibleProps],
  );

  const resizeColumn = useCallback(
    (columnId: string, width: number, fallback: number) => {
      const normalized = normalizeTableColumnWidth(width, fallback);
      setColumnWidths((prev) => {
        const key = tableColumnWidthKey(typeId, columnId);
        const legacyKey = legacyTableColumnWidthKey(typeId, columnId);
        if (prev[key] === normalized) return prev;
        const next = { ...prev, [key]: normalized };
        delete next[legacyKey];
        return next;
      });
    },
    [setColumnWidths, typeId],
  );

  const toggleProperty = useCallback((propId: string, visible: boolean) => {
    setHiddenPropIds((prev) => {
      const next = new Set(prev);
      if (visible) next.delete(propId);
      else next.add(propId);
      return next;
    });
  }, []);

  const moveProperty = useCallback(
    (sourceId: string, targetId: string) => {
      if (sourceId === targetId) return;
      setPropertyOrder((prev) => {
        const base = prev.length > 0 ? prev : rawUserPropIds;
        const next = base.filter((id) => rawUserPropIds.includes(id));
        for (const id of rawUserPropIds) {
          if (!next.includes(id)) next.push(id);
        }

        const from = next.indexOf(sourceId);
        const to = next.indexOf(targetId);
        if (from < 0 || to < 0) return prev;

        const [moved] = next.splice(from, 1);
        if (moved == null) return prev;
        next.splice(to, 0, moved);
        return next;
      });
    },
    [rawUserPropIds],
  );

  const endColumnDrag = useCallback(() => {
    setDraggedColumnPropId(null);
    setColumnDropTargetPropId(null);
  }, []);

  return {
    propsQuery,
    rawUserProps,
    userProps,
    visibleProps,
    hiddenPropIds,
    toggleProperty,
    moveProperty,
    nameColumnWidth,
    propertyColumnWidths,
    tablePixelWidth,
    resizeColumn,
    draggedColumnPropId,
    setDraggedColumnPropId,
    columnDropTargetPropId,
    setColumnDropTargetPropId,
    endColumnDrag,
  };
}

function isUserKind(k: PropertyKind): boolean {
  return (
    k === 'string' ||
    k === 'number' ||
    k === 'boolean' ||
    k === 'null' ||
    k === 'array' ||
    k === 'object'
  );
}
