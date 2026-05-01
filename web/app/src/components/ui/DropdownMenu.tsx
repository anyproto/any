import { forwardRef, type ComponentPropsWithoutRef, type ElementRef } from 'react';
import * as RadixMenu from '@radix-ui/react-dropdown-menu';
import { Check } from 'lucide-react';
import { cn } from '@/lib/cn';

export const DropdownMenu = RadixMenu.Root;
export const DropdownMenuTrigger = RadixMenu.Trigger;
export const DropdownMenuGroup = RadixMenu.Group;

const menuSurface = [
  'min-w-[8rem] z-[var(--z-popover)]',
  'rounded-md border border-foreground/10 bg-background p-1 shadow-md',
  'focus-visible:outline-none',
] as const;

const menuItem = [
  'relative flex cursor-pointer select-none items-center gap-2',
  'rounded-sm px-2 py-1.5 text-sm text-foreground outline-none',
  'data-[highlighted]:bg-foreground/5',
  'data-[disabled]:pointer-events-none data-[disabled]:opacity-50',
] as const;

export const DropdownMenuContent = forwardRef<
  ElementRef<typeof RadixMenu.Content>,
  ComponentPropsWithoutRef<typeof RadixMenu.Content>
>(function DropdownMenuContent({ className, sideOffset = 4, ...rest }, ref) {
  return (
    <RadixMenu.Portal>
      <RadixMenu.Content
        ref={ref}
        sideOffset={sideOffset}
        className={cn(menuSurface, className)}
        {...rest}
      />
    </RadixMenu.Portal>
  );
});

export const DropdownMenuItem = forwardRef<
  ElementRef<typeof RadixMenu.Item>,
  ComponentPropsWithoutRef<typeof RadixMenu.Item>
>(function DropdownMenuItem({ className, ...rest }, ref) {
  return <RadixMenu.Item ref={ref} className={cn(menuItem, className)} {...rest} />;
});

export const DropdownMenuCheckboxItem = forwardRef<
  ElementRef<typeof RadixMenu.CheckboxItem>,
  ComponentPropsWithoutRef<typeof RadixMenu.CheckboxItem>
>(function DropdownMenuCheckboxItem({ className, children, ...rest }, ref) {
  return (
    <RadixMenu.CheckboxItem
      ref={ref}
      className={cn(menuItem, 'pl-7', className)}
      {...rest}
    >
      <span className="absolute left-2 inline-flex h-4 w-4 items-center justify-center">
        <RadixMenu.ItemIndicator>
          <Check className="h-3.5 w-3.5" aria-hidden />
        </RadixMenu.ItemIndicator>
      </span>
      {children}
    </RadixMenu.CheckboxItem>
  );
});

export const DropdownMenuLabel = forwardRef<
  ElementRef<typeof RadixMenu.Label>,
  ComponentPropsWithoutRef<typeof RadixMenu.Label>
>(function DropdownMenuLabel({ className, ...rest }, ref) {
  return (
    <RadixMenu.Label
      ref={ref}
      className={cn('px-2 py-1 text-xs font-medium text-foreground/60', className)}
      {...rest}
    />
  );
});

export const DropdownMenuSeparator = forwardRef<
  ElementRef<typeof RadixMenu.Separator>,
  ComponentPropsWithoutRef<typeof RadixMenu.Separator>
>(function DropdownMenuSeparator({ className, ...rest }, ref) {
  return (
    <RadixMenu.Separator
      ref={ref}
      className={cn('my-1 h-px bg-foreground/10', className)}
      {...rest}
    />
  );
});

export const DropdownMenuShortcut = ({ className, ...rest }: { className?: string; children?: React.ReactNode }) => (
  <span className={cn('ml-auto text-xs text-foreground/50', className)} {...rest} />
);
