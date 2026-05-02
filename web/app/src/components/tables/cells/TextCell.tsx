import { useEffect, useRef, useState } from 'react';
import { Input } from '@/components/ui/Input';
import { cn } from '@/lib/cn';

interface TextCellProps {
  value: string;
  placeholder?: string;
  /** Auto-enter edit mode on mount. Used by add-row → focus name. */
  autoEdit?: boolean;
  onCommit: (next: string) => void | Promise<void>;
}

/**
 * Display + inline-edit text cell. Click / Enter / F2 enters edit;
 * Enter / blur commits, Esc cancels.
 */
export function TextCell({ value, placeholder, autoEdit, onCommit }: TextCellProps) {
  const [editing, setEditing] = useState(autoEdit ?? false);

  if (editing) {
    return (
      <CellInput
        initial={value}
        placeholder={placeholder ?? ''}
        onCommit={(v) => {
          setEditing(false);
          if (v !== value) void onCommit(v);
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
        'block h-full w-full truncate px-2 py-1 text-left text-[13px]',
        value ? 'text-foreground' : 'text-foreground/40',
        'hover:bg-foreground/[0.03]',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      {value || placeholder || '—'}
    </button>
  );
}

function CellInput({
  initial,
  placeholder,
  onCommit,
  onCancel,
}: {
  initial: string;
  placeholder: string;
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
      className="h-full w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-[13px] focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
      value={value}
      placeholder={placeholder}
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
