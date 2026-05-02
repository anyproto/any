import { useEffect, useRef, useState } from 'react';
import { Textarea } from '@/components/ui/Input';
import { cn } from '@/lib/cn';

interface LongTextCellProps {
  value: string;
  onCommit: (next: string) => void | Promise<void>;
}

/**
 * Multi-line text cell. Display mode collapses to a single line with
 * ellipsis. Edit mode is a textarea: Shift+Enter inserts a newline,
 * Enter commits, Esc cancels, blur commits.
 */
export function LongTextCell({ value, onCommit }: LongTextCellProps) {
  const [editing, setEditing] = useState(false);

  if (editing) {
    return (
      <CellTextarea
        initial={value}
        onCommit={(v) => {
          setEditing(false);
          if (v !== value) void onCommit(v);
        }}
        onCancel={() => setEditing(false)}
      />
    );
  }
  // Show the first line only in collapsed mode; the user expands by editing.
  const firstLine = value.split('\n', 1)[0] ?? '';
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
      title={value}
      className={cn(
        'block h-full w-full truncate px-2 py-1 text-left text-[13px]',
        value ? 'text-foreground' : 'text-foreground/40',
        'hover:bg-foreground/[0.03]',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      {firstLine || '—'}
    </button>
  );
}

function CellTextarea({
  initial,
  onCommit,
  onCancel,
}: {
  initial: string;
  onCommit: (v: string) => void;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(initial);
  const ref = useRef<HTMLTextAreaElement>(null);
  const cancelledRef = useRef(false);

  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
  }, []);

  return (
    <Textarea
      ref={ref}
      rows={4}
      className="block h-auto min-h-[5rem] w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-[13px] focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && !e.shiftKey) {
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
