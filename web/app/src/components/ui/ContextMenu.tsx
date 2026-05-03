import { forwardRef, type ComponentPropsWithoutRef, type ElementRef } from 'react';
import * as RadixContextMenu from '@radix-ui/react-context-menu';
import { Check } from 'lucide-react';
import { cn } from '@/lib/cn';

export const ContextMenu = RadixContextMenu.Root;
export const ContextMenuTrigger = RadixContextMenu.Trigger;
export const ContextMenuGroup = RadixContextMenu.Group;

const menuSurface = [
  'min-w-[8rem] z-[var(--z-popover)]',
  'rounded-md border border-foreground/10 bg-background p-1 shadow-md',
] as const;

const menuItem = [
  'relative flex cursor-pointer select-none items-center gap-2',
  'rounded-sm px-2 py-1.5 text-sm text-foreground outline-none',
  'data-[highlighted]:bg-foreground/5',
  'data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
] as const;

export const ContextMenuContent = forwardRef<
  ElementRef<typeof RadixContextMenu.Content>,
  ComponentPropsWithoutRef<typeof RadixContextMenu.Content>
>(function ContextMenuContent({ className, ...rest }, ref) {
  return (
    <RadixContextMenu.Portal>
      <RadixContextMenu.Content
        ref={ref}
        className={cn(menuSurface, className)}
        {...rest}
      />
    </RadixContextMenu.Portal>
  );
});

export const ContextMenuItem = forwardRef<
  ElementRef<typeof RadixContextMenu.Item>,
  ComponentPropsWithoutRef<typeof RadixContextMenu.Item>
>(function ContextMenuItem({ className, ...rest }, ref) {
  return (
    <RadixContextMenu.Item ref={ref} className={cn(menuItem, className)} {...rest} />
  );
});

export const ContextMenuCheckboxItem = forwardRef<
  ElementRef<typeof RadixContextMenu.CheckboxItem>,
  ComponentPropsWithoutRef<typeof RadixContextMenu.CheckboxItem>
>(function ContextMenuCheckboxItem({ className, children, ...rest }, ref) {
  return (
    <RadixContextMenu.CheckboxItem
      ref={ref}
      className={cn(menuItem, 'pl-7', className)}
      {...rest}
    >
      <span className="absolute left-2 inline-flex h-4 w-4 items-center justify-center">
        <RadixContextMenu.ItemIndicator>
          <Check className="h-3.5 w-3.5" aria-hidden />
        </RadixContextMenu.ItemIndicator>
      </span>
      {children}
    </RadixContextMenu.CheckboxItem>
  );
});

export const ContextMenuSeparator = forwardRef<
  ElementRef<typeof RadixContextMenu.Separator>,
  ComponentPropsWithoutRef<typeof RadixContextMenu.Separator>
>(function ContextMenuSeparator({ className, ...rest }, ref) {
  return (
    <RadixContextMenu.Separator
      ref={ref}
      className={cn('my-1 h-px bg-foreground/10', className)}
      {...rest}
    />
  );
});

export const ContextMenuLabel = forwardRef<
  ElementRef<typeof RadixContextMenu.Label>,
  ComponentPropsWithoutRef<typeof RadixContextMenu.Label>
>(function ContextMenuLabel({ className, ...rest }, ref) {
  return (
    <RadixContextMenu.Label
      ref={ref}
      className={cn('px-2 py-1 text-xs font-medium text-foreground/60', className)}
      {...rest}
    />
  );
});
