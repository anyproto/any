import { forwardRef, type ComponentPropsWithoutRef, type ElementRef } from 'react';
import * as RadixLabel from '@radix-ui/react-label';
import { cn } from '@/lib/cn';

export type LabelProps = ComponentPropsWithoutRef<typeof RadixLabel.Root>;

export const Label = forwardRef<ElementRef<typeof RadixLabel.Root>, LabelProps>(
  function Label({ className, ...rest }, ref) {
    return (
      <RadixLabel.Root
        ref={ref}
        className={cn(
          'text-xs font-medium text-foreground/80 select-none',
          'peer-disabled:cursor-not-allowed peer-disabled:opacity-50',
          className,
        )}
        {...rest}
      />
    );
  },
);
