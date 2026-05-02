import type { ReactNode } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/cn';

interface SectionHeaderProps {
  label: string;
  /** Optional trailing count badge. Hidden if undefined. */
  count?: number | undefined;
  /**
   * Optional trailing action node (e.g. an Add button). Rendered
   * outside the toggle button so its own clicks don't bubble.
   * Wins over `count` when both are provided.
   */
  action?: ReactNode;
  expanded: boolean;
  onToggle: () => void;
}

/**
 * Small uppercase header with a left chevron, used to group
 * collapsible content in pane 2 (Pages, Types, …).
 */
export function SectionHeader({ label, count, action, expanded, onToggle }: SectionHeaderProps) {
  const Icon = expanded ? ChevronDown : ChevronRight;
  return (
    <div className="flex w-full items-center gap-1 px-2 py-1">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className={cn(
          'flex flex-1 items-center gap-1 text-[11px] font-medium uppercase',
          'leading-[14px] tracking-[0.05em]',
          'text-foreground/40 hover:text-foreground/60',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent rounded',
        )}
      >
        <Icon className="h-3 w-3 shrink-0" aria-hidden />
        <span className="flex-1 text-left">{label}</span>
      </button>
      {action ?? (
        count != null && count > 0 ? (
          <span className="text-[11px] text-foreground/40">{count}</span>
        ) : null
      )}
    </div>
  );
}
