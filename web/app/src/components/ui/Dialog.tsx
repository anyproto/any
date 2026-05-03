import {
  forwardRef,
  type ComponentPropsWithoutRef,
  type ElementRef,
  type ReactNode,
} from 'react';
import * as RadixDialog from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import { cn } from '@/lib/cn';

export const Dialog = RadixDialog.Root;
export const DialogTrigger = RadixDialog.Trigger;
export const DialogClose = RadixDialog.Close;

const Overlay = forwardRef<
  ElementRef<typeof RadixDialog.Overlay>,
  ComponentPropsWithoutRef<typeof RadixDialog.Overlay>
>(function DialogOverlay({ className, ...rest }, ref) {
  return (
    <RadixDialog.Overlay
      ref={ref}
      className={cn(
        'fixed inset-0 z-[var(--z-dialog)]',
        'bg-foreground/30 backdrop-blur-sm',
        'data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0',
        className,
      )}
      {...rest}
    />
  );
});

interface DialogContentProps
  extends ComponentPropsWithoutRef<typeof RadixDialog.Content> {
  /** Visually hidden close button slot? Defaults to a visible × button. */
  hideCloseButton?: boolean;
  children?: ReactNode;
}

export const DialogContent = forwardRef<
  ElementRef<typeof RadixDialog.Content>,
  DialogContentProps
>(function DialogContent({ className, hideCloseButton, children, ...rest }, ref) {
  return (
    <RadixDialog.Portal>
      <Overlay />
      <RadixDialog.Content
        ref={ref}
        className={cn(
          'fixed left-1/2 top-1/2 z-[var(--z-dialog)] w-full max-w-md',
          '-translate-x-1/2 -translate-y-1/2',
          'rounded-lg border border-foreground/10 bg-background p-5 shadow-lg',
          'focus-visible:outline-none',
          className,
        )}
        {...rest}
      >
        {children}
        {!hideCloseButton && (
          <RadixDialog.Close
            aria-label="Close"
            className={cn(
              'absolute right-3 top-3 rounded-md p-1 text-foreground/60',
              'hover:bg-foreground/5 hover:text-foreground',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <X className="h-4 w-4" aria-hidden />
          </RadixDialog.Close>
        )}
      </RadixDialog.Content>
    </RadixDialog.Portal>
  );
});

export const DialogTitle = forwardRef<
  ElementRef<typeof RadixDialog.Title>,
  ComponentPropsWithoutRef<typeof RadixDialog.Title>
>(function DialogTitle({ className, ...rest }, ref) {
  return (
    <RadixDialog.Title
      ref={ref}
      className={cn('text-base font-semibold tracking-tight', className)}
      {...rest}
    />
  );
});

export const DialogDescription = forwardRef<
  ElementRef<typeof RadixDialog.Description>,
  ComponentPropsWithoutRef<typeof RadixDialog.Description>
>(function DialogDescription({ className, ...rest }, ref) {
  return (
    <RadixDialog.Description
      ref={ref}
      className={cn('mt-1 text-sm text-foreground/70', className)}
      {...rest}
    />
  );
});

export function DialogHeader({ className, ...rest }: { className?: string; children?: ReactNode }) {
  return <div className={cn('mb-3 flex flex-col', className)} {...rest} />;
}

export function DialogFooter({ className, ...rest }: { className?: string; children?: ReactNode }) {
  return (
    <div
      className={cn('mt-5 flex items-center justify-end gap-2', className)}
      {...rest}
    />
  );
}
