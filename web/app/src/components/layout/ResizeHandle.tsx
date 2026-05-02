import { PanelResizeHandle } from 'react-resizable-panels';
import { cn } from '@/lib/cn';

/**
 * Slim 1px divider between panes; the hit area is a 5px-wide stripe
 * around it via the negative-margin trick so the user has actual room
 * to grab. Hover and drag states are token-driven.
 */
export function ResizeHandle({ className }: { className?: string }) {
  return (
    <PanelResizeHandle
      className={cn(
        // Slim visible divider
        'group relative w-px bg-foreground/10 transition-colors',
        // Wider invisible hit area (4px each side)
        'before:absolute before:inset-y-0 before:-left-1 before:right-0 before:w-2 before:cursor-col-resize',
        // Hover / active feedback
        'hover:bg-foreground/30 data-[resize-handle-active]:bg-accent',
        className,
      )}
    />
  );
}
