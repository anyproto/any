import { Check } from 'lucide-react';
import { cn } from '@/lib/cn';

interface BoolCellProps {
  value: boolean | null;
  onCommit: (next: boolean) => void | Promise<void>;
}

/**
 * Display + toggle cell. No edit mode — click toggles.
 * Null is shown as a faint dash; clicking sets to true (then false).
 */
export function BoolCell({ value, onCommit }: BoolCellProps) {
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={value === true}
      onClick={() => void onCommit(!value)}
      onKeyDown={(e) => {
        if (e.key === ' ' || e.key === 'Enter') {
          e.preventDefault();
          void onCommit(!value);
        }
      }}
      className={cn(
        'flex h-full w-full items-center justify-center px-2 py-1',
        'hover:bg-foreground/[0.03]',
        'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
      )}
    >
      <span
        aria-hidden
        className={cn(
          'inline-flex h-4 w-4 items-center justify-center rounded border',
          value
            ? 'border-accent bg-accent text-background'
            : 'border-foreground/20 text-transparent',
        )}
      >
        <Check className="h-3 w-3" />
      </span>
    </button>
  );
}
