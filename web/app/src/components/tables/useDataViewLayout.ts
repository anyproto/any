import { useCallback } from 'react';
import { useAtom } from 'jotai';
import {
  normalizeTableViewLayout,
  tableViewLayoutsAtom,
  type TableViewLayout,
} from '@/atoms';

export function useDataViewLayout(typeId: string) {
  const [viewLayouts, setViewLayouts] = useAtom(tableViewLayoutsAtom);
  const viewLayout = normalizeTableViewLayout(viewLayouts[typeId]);

  const setViewLayout = useCallback(
    (layout: TableViewLayout) => {
      setViewLayouts((prev) => {
        if (prev[typeId] === layout) return prev;
        return { ...prev, [typeId]: layout };
      });
    },
    [setViewLayouts, typeId],
  );

  return { viewLayout, setViewLayout };
}
