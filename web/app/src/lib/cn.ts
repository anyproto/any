import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/**
 * Compose Tailwind class names with conflict resolution.
 *
 * Use everywhere classes are computed. Lets us write
 * `<Button className={cn('text-foreground', className)} />` and have
 * caller-provided utilities override component defaults.
 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
