import { forwardRef, type ComponentPropsWithoutRef, type ElementRef } from 'react';
import * as RadixPopover from '@radix-ui/react-popover';
import { cn } from '@/lib/cn';

export const Popover = RadixPopover.Root;
export const PopoverTrigger = RadixPopover.Trigger;
export const PopoverAnchor = RadixPopover.Anchor;
export const PopoverClose = RadixPopover.Close;

export const PopoverContent = forwardRef<
  ElementRef<typeof RadixPopover.Content>,
  ComponentPropsWithoutRef<typeof RadixPopover.Content>
>(function PopoverContent({ className, sideOffset = 6, align = 'center', ...rest }, ref) {
  return (
    <RadixPopover.Portal>
      <RadixPopover.Content
        ref={ref}
        sideOffset={sideOffset}
        align={align}
        className={cn(
          'z-[var(--z-popover)] w-72',
          'rounded-md border border-foreground/10 bg-background p-3 shadow-md',
          'focus-visible:outline-none',
          className,
        )}
        {...rest}
      />
    </RadixPopover.Portal>
  );
});
