import { useEffect, useRef, useState } from 'react';
import { Input } from '@/components/ui';
import { cn } from '@/lib/cn';

interface DateCellProps {
  /** ISO 8601 day string ("YYYY-MM-DD") or empty / null. */
  value: string | null;
  onCommit: (next: string) => void | Promise<void>;
}

/**
 * Day-precision date cell. Storage is the ISO 8601 day string.
 * Display is the user's locale; edit uses the browser's date picker.
 */
export function DateCell({ value, onCommit }: DateCellProps) {
  const [editing, setEditing] = useState(false);
  const iso = typeof value === 'string' ? value : '';

  if (editing) {
    return (
      <DateInput
        initial={iso}
        onCommit={(v) => {
          setEditing(false);
          if (v !== iso) void onCommit(v);
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
        'block h-full w-full truncate px-2 py-1 text-left text-[13px] tabular-nums',
        iso ? 'text-foreground' : 'text-foreground/40',
        'hover:bg-foreground/[0.03]',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      {formatDate(iso)}
    </button>
  );
}

function DateInput({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string;
  onCommit: (v: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  const ref = useRef<HTMLInputElement>(null);
  const cancelledRef = useRef(false);

  useEffect(() => {
    ref.current?.focus();
  }, []);

  return (
    <Input
      ref={ref}
      type="date"
      value={value}
      className="h-full w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-[13px] tabular-nums focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
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

function formatDate(iso: string): string {
  if (!iso) return '—';
  // ISO day string YYYY-MM-DD parses unambiguously when interpreted as UTC.
  // Add the day boundary so we don't drift into the previous day in UTC-
  // negative locales.
  const d = new Date(`${iso}T00:00:00`);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
  });
}
