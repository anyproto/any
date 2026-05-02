import { ChevronDown, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/cn';

interface SectionHeaderProps {
  label: string;
  /** Optional trailing count badge. Hidden if undefined. */
  count?: number | undefined;
  expanded: boolean;
  onToggle: () => void;
}

/**
 * Small uppercase header with a left chevron, used to group
 * collapsible content in pane 2 (Pages, Types, …).
 */
export function SectionHeader({ label, count, expanded, onToggle }: SectionHeaderProps) {
  const Icon = expanded ? ChevronDown : ChevronRight;
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={expanded}
      className={cn(
        'flex w-full items-center gap-1 px-2 py-1 text-xs font-medium uppercase tracking-wide',
        'text-foreground/40 hover:text-foreground/60',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent rounded',
      )}
    >
      <Icon className="h-3 w-3 shrink-0" aria-hidden />
      <span className="flex-1 text-left">{label}</span>
      {count != null && count > 0 && (
        <span className="text-foreground/40">{count}</span>
      )}
    </button>
  );
}
