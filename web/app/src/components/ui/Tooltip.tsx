import { forwardRef, type ComponentPropsWithoutRef, type ElementRef, type ReactNode } from 'react';
import * as RadixTooltip from '@radix-ui/react-tooltip';
import { cn } from '@/lib/cn';

export const TooltipProvider = RadixTooltip.Provider;

export const Tooltip = RadixTooltip.Root;
export const TooltipTrigger = RadixTooltip.Trigger;

export const TooltipContent = forwardRef<
  ElementRef<typeof RadixTooltip.Content>,
  ComponentPropsWithoutRef<typeof RadixTooltip.Content>
>(function TooltipContent({ className, sideOffset = 4, ...rest }, ref) {
  return (
    <RadixTooltip.Portal>
      <RadixTooltip.Content
        ref={ref}
        sideOffset={sideOffset}
        className={cn(
          'z-[var(--z-popover)]',
          'rounded border border-foreground/10 bg-foreground px-2 py-1 text-xs text-background',
          'shadow-md',
          className,
        )}
        {...rest}
      />
    </RadixTooltip.Portal>
  );
});

/**
 * Convenience wrapper: <SimpleTooltip text="Save (Cmd+S)"><Button .../></SimpleTooltip>
 * Most call sites need only this; reach for the lower-level parts when
 * customizing side, alignment, or timing.
 */
export function SimpleTooltip({
  text,
  children,
  delayDuration = 200,
  side = 'top',
}: {
  text: ReactNode;
  children: ReactNode;
  delayDuration?: number;
  side?: 'top' | 'right' | 'bottom' | 'left';
}) {
  return (
    <TooltipProvider delayDuration={delayDuration}>
      <Tooltip>
        <TooltipTrigger asChild>{children}</TooltipTrigger>
        <TooltipContent side={side}>{text}</TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}
