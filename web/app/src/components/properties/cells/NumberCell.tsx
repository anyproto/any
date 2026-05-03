import { useEffect, useRef, useState } from 'react';
import { Input } from '@/components/ui';
import { cn } from '@/lib/cn';

interface NumberCellProps {
  value: number | null;
  onCommit: (next: number | null) => void | Promise<void>;
}

/**
 * Display + inline-edit numeric cell. Empty input commits null.
 * Invalid (non-numeric) input is silently ignored on commit.
 */
export function NumberCell({ value, onCommit }: NumberCellProps) {
  const [editing, setEditing] = useState(false);

  if (editing) {
    return (
      <NumberInput
        initial={value == null ? '' : String(value)}
        onCommit={(text) => {
          setEditing(false);
          const trimmed = text.trim();
          if (trimmed === '') {
            if (value !== null) void onCommit(null);
            return;
          }
          const n = Number(trimmed);
          if (Number.isNaN(n)) return; // ignore garbage
          if (n !== value) void onCommit(n);
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
        'block h-full w-full truncate px-2 py-1 text-right text-[13px] tabular-nums',
        value == null ? 'text-foreground/40' : 'text-foreground',
        'hover:bg-foreground/[0.03]',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      {value == null ? '—' : String(value)}
    </button>
  );
}

function NumberInput({
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
    ref.current?.select();
  }, []);

  return (
    <Input
      ref={ref}
      type="text"
      inputMode="decimal"
      className="h-full w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-right text-[13px] tabular-nums focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
      value={value}
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
