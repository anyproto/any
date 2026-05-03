/**
 * Barrel export for the UI primitives.
 *
 * Convention: import primitives from '@/components/ui' rather than the
 * individual files. Keeps imports tidy and lets us refactor file
 * names without churning consumers.
 */

export { Button } from './Button';
export type { ButtonProps } from './Button';

export { Input, Textarea } from './Input';
export type { InputProps, TextareaProps } from './Input';

export { Label } from './Label';
export type { LabelProps } from './Label';

export {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogClose,
  DialogTitle,
  DialogDescription,
  DialogHeader,
  DialogFooter,
} from './Dialog';

export {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuGroup,
} from './DropdownMenu';

export {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuCheckboxItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuGroup,
} from './ContextMenu';

export {
  Popover,
  PopoverTrigger,
  PopoverAnchor,
  PopoverClose,
  PopoverContent,
} from './Popover';

export {
  TooltipProvider,
  Tooltip,
  TooltipTrigger,
  TooltipContent,
  SimpleTooltip,
} from './Tooltip';

export { toast } from './Toast';
export type { ExternalToast } from './Toast';
