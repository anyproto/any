import { useEffect, useRef, useState } from 'react';
import { Mail } from 'lucide-react';
import { Input } from '@/components/ui/Input';
import { cn } from '@/lib/cn';

interface EmailCellProps {
  value: string;
  onCommit: (next: string) => void | Promise<void>;
}

/**
 * Email cell. Display mode renders a small mailto link if the value
 * contains an `@`; otherwise just text. Edit mode is a plain text
 * input with `type=email`.
 */
export function EmailCell({ value, onCommit }: EmailCellProps) {
  const [editing, setEditing] = useState(false);
  const valid = /\S+@\S+\.\S+/.test(value);

  if (editing) {
    return (
      <EmailInput
        initial={value}
        onCommit={(v) => {
          setEditing(false);
          if (v !== value) void onCommit(v);
        }}
        onCancel={() => setEditing(false)}
      />
    );
  }

  return (
    <div
      className={cn(
        'flex h-full w-full items-center gap-1 px-2 py-1',
        'hover:bg-foreground/[0.03]',
      )}
    >
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
          'flex-1 truncate text-left text-[13px]',
          value ? 'text-foreground' : 'text-foreground/40',
          'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset rounded',
        )}
      >
        {value || '—'}
      </button>
      {valid && (
        <a
          href={`mailto:${value}`}
          aria-label="Send email"
          className="text-foreground/40 hover:text-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent rounded"
          onClick={(e) => e.stopPropagation()}
        >
          <Mail className="h-3.5 w-3.5" aria-hidden />
        </a>
      )}
    </div>
  );
}

function EmailInput({
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
      type="email"
      inputMode="email"
      autoComplete="off"
      className="h-full w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-[13px] focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
      value={value}
      placeholder="name@domain"
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
