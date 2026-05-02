import {
  useObjectChildren,
  NAV_ROOT_PARENT_ID,
} from '@/lib/api/objects';
import { ApiError } from '@/lib/api/client';
import { ObjectRow } from './ObjectRow';

/**
 * The root of the object tree for the active space. Renders the
 * objects whose `nav.parentId === ""`. Each ObjectRow handles its own
 * lazy-loaded children.
 *
 * State surfaces:
 *   loading  → 3 skeleton rows
 *   error    → tiny inline error card with the API code
 *   empty    → friendly nudge ("No objects yet — create one")
 *   ok       → list of rows
 */
export function ObjectTree({ spaceId }: { spaceId: string }) {
  const q = useObjectChildren(spaceId, NAV_ROOT_PARENT_ID);

  if (q.isPending) {
    return (
      <ul aria-busy="true" className="space-y-1.5 px-2 py-1">
        {[0, 1, 2].map((i) => (
          <li
            key={i}
            aria-hidden
            className="h-5 animate-pulse rounded bg-foreground/10"
          />
        ))}
      </ul>
    );
  }

  if (q.isError) {
    const code = q.error instanceof ApiError ? q.error.code : 'unknown';
    const message = q.error instanceof Error ? q.error.message : 'Failed to load objects';
    return (
      <div
        role="alert"
        className="m-2 rounded-md border border-destructive/30 bg-destructive/[0.06] p-3 text-xs text-foreground"
      >
        <p className="font-medium text-destructive">Couldn&rsquo;t load objects</p>
        <p className="mt-1 text-foreground/70">
          <code className="font-mono">{code}</code> — {message}
        </p>
      </div>
    );
  }

  if (!q.data || q.data.length === 0) {
    return (
      <div className="px-3 py-6 text-center text-xs text-foreground/50">
        No objects yet —
        <br />
        create one with <span className="font-medium text-foreground/80">+ New</span>.
      </div>
    );
  }

  return (
    <ul className="px-1 py-1">
      {q.data.map((obj) => (
        <ObjectRow key={obj.id} spaceId={spaceId} obj={obj} depth={0} />
      ))}
    </ul>
  );
}
