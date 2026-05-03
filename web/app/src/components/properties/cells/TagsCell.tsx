import { useEffect, useRef, useState } from 'react';
import { Input } from '@/components/ui';
import { cn } from '@/lib/cn';

interface TagsCellProps {
  value: readonly string[];
  onCommit: (next: string[]) => void | Promise<void>;
}

/**
 * Tags cell. Display mode renders pill chips. Edit mode is a comma-
 * separated input. On commit we trim, drop empties, and dedupe.
 */
export function TagsCell({ value, onCommit }: TagsCellProps) {
  const [editing, setEditing] = useState(false);
  const tags = Array.isArray(value) ? value : [];

  if (editing) {
    return (
      <TagsInput
        initial={tags.join(', ')}
        onCommit={(text) => {
          setEditing(false);
          const next = parseTags(text);
          if (!sameTags(next, tags)) void onCommit(next);
        }}
        onCancel={() => setEditing(false)}
      />
    );
  }

  return (
    <button
      type="button"
      onClick={() => setEditing(true)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === 'F2') {
          e.preventDefault();
          setEditing(true);
        }
      }}
      className={cn(
        'flex h-full w-full items-center gap-1 truncate px-2 py-1 text-left',
        'hover:bg-foreground/[0.03]',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      {tags.length === 0 ? (
        <span className="text-[13px] text-foreground/40">—</span>
      ) : (
        <span className="flex flex-wrap items-center gap-1 overflow-hidden">
          {tags.map((t, i) => (
            <span
              key={`${t}-${i}`}
              className="inline-flex h-5 items-center rounded-full bg-foreground/10 px-2 text-[11px] font-medium text-foreground/80"
            >
              {t}
            </span>
          ))}
        </span>
      )}
    </button>
  );
}

function TagsInput({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string;
  onCommit: (text: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  const ref = useRef<HTMLInputElement>(null);
  const cancelledRef = useRef(false);

  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
  }, []);

  return (
    <Input
      ref={ref}
      type="text"
      autoComplete="off"
      className="h-full w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-[13px] focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
      value={value}
      placeholder="tag, tag, tag…"
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          onCommit(value);
        } else if (e.key === 'Escape') {
          e.preventDefault();
          cancelledRef.current = true;
          onCancel();
        }
        e.stopPropagation();
      }}
      onBlur={() => {
        if (cancelledRef.current) {
          cancelledRef.current = false;
          return;
        }
        onCommit(value);
      }}
    />
  );
}

/** Parse a comma-separated string → trimmed, non-empty, deduped tags. */
export function parseTags(text: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of text.split(',')) {
    const t = raw.trim();
    if (!t) continue;
    if (seen.has(t)) continue;
    seen.add(t);
    out.push(t);
  }
  return out;
}

function sameTags(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i] !== b[i]) return false;
  }
  return true;
}
