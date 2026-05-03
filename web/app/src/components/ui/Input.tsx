import { forwardRef, type InputHTMLAttributes, type TextareaHTMLAttributes } from 'react';
import { cn } from '@/lib/cn';

const baseField = [
  'w-full rounded-md border border-foreground/15 bg-background',
  'px-2.5 py-1.5 text-sm text-foreground placeholder:text-foreground/40',
  'transition-colors',
  'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background',
  'disabled:cursor-not-allowed disabled:opacity-50',
] as const;

export type InputProps = InputHTMLAttributes<HTMLInputElement>;

export const Input = forwardRef<HTMLInputElement, InputProps>(
  function Input({ className, type = 'text', ...rest }, ref) {
    return (
      <input
        ref={ref}
        type={type}
        className={cn(baseField, 'h-8', className)}
        {...rest}
      />
    );
  },
);

export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(
  function Textarea({ className, rows = 3, ...rest }, ref) {
    return (
      <textarea
        ref={ref}
        rows={rows}
        className={cn(baseField, 'resize-y', className)}
        {...rest}
      />
    );
  },
);
