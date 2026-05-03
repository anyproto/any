import { useEffect, useState, type ReactNode } from 'react';

const MIN_WIDTH = 800;

/**
 * Below MIN_WIDTH, renders a centered notice instead of the children.
 * v1 is desktop-only; mobile/responsive layout is a v2 concern.
 *
 * Listens to window resize via matchMedia (cheaper than 'resize').
 */
export function MinWidthGuard({ children }: { children: ReactNode }) {
  const tooNarrow = useTooNarrow();
  if (tooNarrow) return <NarrowNotice />;
  return <>{children}</>;
}

function useTooNarrow(): boolean {
  const [tooNarrow, setTooNarrow] = useState(() =>
    typeof window !== 'undefined' ? window.innerWidth < MIN_WIDTH : false,
  );
  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MIN_WIDTH - 1}px)`);
    const onChange = (e: MediaQueryListEvent) => setTooNarrow(e.matches);
    setTooNarrow(mql.matches);
    mql.addEventListener('change', onChange);
    return () => mql.removeEventListener('change', onChange);
  }, []);
  return tooNarrow;
}

function NarrowNotice() {
  return (
    <div className="flex h-screen items-center justify-center p-8">
      <div className="max-w-sm rounded-lg border border-foreground/10 bg-background p-6 text-center shadow-sm">
        <p className="text-sm text-foreground">
          <strong>any</strong> is desktop-only in v1.
        </p>
        <p className="mt-2 text-sm text-foreground/60">
          Open this in a wider window (≥ 800 px). Responsive layout is on the v2 roadmap.
        </p>
      </div>
    </div>
  );
}
