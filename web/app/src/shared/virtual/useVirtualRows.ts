import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

interface UseVirtualRowsArgs {
  count: number;
  rowHeight: number;
  overscan?: number;
}

export function useVirtualRows({ count, rowHeight, overscan = 8 }: UseVirtualRowsArgs) {
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(0);

  const measure = useCallback(() => {
    setViewportHeight(scrollRef.current?.clientHeight ?? 0);
  }, []);

  useEffect(() => {
    measure();
    const node = scrollRef.current;
    if (!node || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [measure]);

  const onScroll = useCallback(() => {
    setScrollTop(scrollRef.current?.scrollTop ?? 0);
  }, []);

  return useMemo(() => {
    const visibleCount = Math.max(1, Math.ceil(viewportHeight / rowHeight));
    const startIndex = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
    const endIndex = Math.min(count, startIndex + visibleCount + overscan * 2);
    return {
      scrollRef,
      onScroll,
      startIndex,
      endIndex,
      paddingTop: startIndex * rowHeight,
      paddingBottom: Math.max(0, (count - endIndex) * rowHeight),
    };
  }, [count, onScroll, overscan, rowHeight, scrollTop, viewportHeight]);
}
