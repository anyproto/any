import { useEffect, useRef, useState } from 'react';
import { ExternalLink } from 'lucide-react';
import { Input } from '@/components/ui';
import { cn } from '@/lib/cn';

interface UrlCellProps {
  value: string;
  onCommit: (next: string) => void | Promise<void>;
}

/**
 * URL cell. Display mode renders an external link if the value
 * parses as a URL; otherwise just shows the raw text.
 * Edit mode is a plain text input.
 */
export function UrlCell({ value, onCommit }: UrlCellProps) {
  const [editing, setEditing] = useState(false);
  const valid = isLikelyUrl(value);

  if (editing) {
    return (
      <UrlInput
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
          href={normaliseUrl(value)}
          target="_blank"
          rel="noopener noreferrer"
          aria-label="Open link"
          className="text-foreground/40 hover:text-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent rounded"
          onClick={(e) => e.stopPropagation()}
        >
          <ExternalLink className="h-3.5 w-3.5" aria-hidden />
        </a>
      )}
    </div>
  );
}

function UrlInput({
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
      type="url"
      inputMode="url"
      autoComplete="off"
      className="h-full w-full rounded-none border-0 bg-foreground/[0.04] px-2 py-1 text-[13px] focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset focus-visible:ring-offset-0"
      value={value}
      placeholder="https://"
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

function isLikelyUrl(value: string): boolean {
  if (!value) return false;
  try {
    new URL(normaliseUrl(value));
    return true;
  } catch {
    return false;
  }
}

function normaliseUrl(value: string): string {
  if (/^https?:\/\//i.test(value)) return value;
  if (/^[\w.-]+\.[a-z]{2,}/i.test(value)) return `https://${value}`;
  return value;
}
