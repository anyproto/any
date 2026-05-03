import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from 'react';
import { Sparkles } from 'lucide-react';
import {
  Button,
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Input,
} from '@/components/ui';
import { cn } from '@/lib/cn';
import { LUCIDE_ICON_PREFIX, lucideIconIdFromToken } from './listIconTokens';

const LazyLucideIcon = lazy(() =>
  import('./ListLucideIconCatalog').then((mod) => ({
    default: mod.LucideIconRenderer,
  })),
);

const LazyIconCatalogPane = lazy(() =>
  import('./ListLucideIconCatalog').then((mod) => ({
    default: mod.ListLucideIconCatalogPane,
  })),
);

export function ListIcon({
  icon,
  className,
}: {
  icon?: string | undefined;
  className?: string | undefined;
}) {
  const lucideIconId = icon ? lucideIconIdFromToken(icon) : undefined;
  if (lucideIconId) {
    return (
      <Suspense fallback={<Sparkles className={cn('shrink-0', className)} aria-hidden />}>
        <LazyLucideIcon id={lucideIconId} className={cn('shrink-0', className)} />
      </Suspense>
    );
  }
  if (icon?.startsWith(LUCIDE_ICON_PREFIX)) {
    return <Sparkles className={cn('shrink-0', className)} aria-hidden />;
  }
  if (icon) {
    return (
      <span
        aria-hidden
        className={cn('inline-flex shrink-0 items-center justify-center text-[1em]', className)}
      >
        {icon}
      </span>
    );
  }
  return <Sparkles className={cn('shrink-0', className)} aria-hidden />;
}

export function RenameListDialog({
  open,
  initialName,
  onOpenChange,
  onRename,
}: {
  open: boolean;
  initialName: string;
  onOpenChange: (open: boolean) => void;
  onRename: (name: string) => void;
}) {
  const [name, setName] = useState(initialName);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const id = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => window.clearTimeout(id);
  }, [open]);

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const next = name.trim();
    if (!next) return;
    onRename(next);
    onOpenChange(false);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) setName(initialName);
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent>
        <form onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Rename list</DialogTitle>
            <DialogDescription>
              This name is stored locally until list metadata writes are available.
            </DialogDescription>
          </DialogHeader>
          <div className="mt-4">
            <label className="text-xs font-medium text-foreground/60" htmlFor="list-name">
              Name
            </label>
            <Input
              ref={inputRef}
              id="list-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="mt-1 h-9"
            />
          </div>
          <DialogFooter className="mt-5">
            <DialogClose asChild>
              <Button type="button" variant="ghost">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit" disabled={name.trim() === ''}>
              Save
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const EMOJI_ICON_CHOICES = [
  '📄',
  '📚',
  '🎬',
  '🎵',
  '✅',
  '💡',
  '⭐',
  '🧩',
  '🗂️',
  '🔖',
  '🎮',
  '🍿',
  '🎨',
  '✍️',
  '📷',
  '🚀',
  '🏠',
  '💼',
  '🛒',
  '🍳',
  '☕',
  '🌍',
  '✈️',
  '🏆',
  '🔒',
  '⚙️',
  '🧠',
  '❤️',
  '🔥',
  '🌙',
];

export function ListIconDialog({
  open,
  icon,
  onOpenChange,
  onSelect,
}: {
  open: boolean;
  icon?: string | undefined;
  onOpenChange: (open: boolean) => void;
  onSelect: (icon: string) => void;
}) {
  const [customIcon, setCustomIcon] = useState(icon ?? '');
  const [tab, setTab] = useState<'icons' | 'emoji'>('icons');

  const choose = (nextIcon: string) => {
    onSelect(nextIcon);
    onOpenChange(false);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          setCustomIcon(icon && !icon.startsWith(LUCIDE_ICON_PREFIX) ? icon : '');
          setTab(icon?.startsWith(LUCIDE_ICON_PREFIX) ? 'icons' : 'emoji');
        }
        onOpenChange(nextOpen);
      }}
    >
      <DialogContent className="max-w-[640px] p-0">
        <DialogHeader className="sr-only">
          <DialogTitle>List icon</DialogTitle>
          <DialogDescription>
            Pick an open-source Lucide icon or an emoji for this list. It is stored locally.
          </DialogDescription>
        </DialogHeader>

        <div className="border-b border-foreground/10 px-4">
          <div role="tablist" aria-label="Icon source" className="flex gap-6">
            <IconPickerTab active={tab === 'icons'} onClick={() => setTab('icons')}>
              Icons
            </IconPickerTab>
            <IconPickerTab active={tab === 'emoji'} onClick={() => setTab('emoji')}>
              Emojis
            </IconPickerTab>
          </div>
        </div>

        {tab === 'icons' ? (
          <Suspense
            fallback={
              <div className="flex h-[260px] items-center justify-center text-foreground/35">
                <Sparkles className="h-5 w-5" aria-hidden />
              </div>
            }
          >
            <LazyIconCatalogPane icon={icon} onChoose={choose} />
          </Suspense>
        ) : (
          <div className="p-4">
            <div className="grid grid-cols-10 gap-1.5">
              {EMOJI_ICON_CHOICES.map((choice) => (
                <button
                  key={choice}
                  type="button"
                  aria-label={`Use ${choice} icon`}
                  onClick={() => choose(choice)}
                  className={cn(
                    'flex h-9 w-9 items-center justify-center rounded-md text-xl',
                    'hover:bg-foreground/7 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                    icon === choice && 'bg-accent/15 ring-1 ring-accent/40',
                  )}
                >
                  {choice}
                </button>
              ))}
            </div>
            <div className="mt-4">
              <label className="text-xs font-medium text-foreground/60" htmlFor="list-icon">
                Custom emoji
              </label>
              <Input
                id="list-icon"
                value={customIcon}
                onChange={(e) => setCustomIcon(e.target.value)}
                maxLength={4}
                className="mt-1 h-9"
                placeholder="Emoji"
              />
            </div>
          </div>
        )}

        <DialogFooter className="mt-0 border-t border-foreground/10 p-4">
          <Button type="button" variant="ghost" onClick={() => choose('')}>
            Clear
          </Button>
          {tab === 'emoji' && (
            <Button
              type="button"
              disabled={customIcon.trim() === ''}
              onClick={() => choose(customIcon.trim())}
            >
              Save
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function IconPickerTab({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: string;
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        'relative h-12 text-sm font-semibold text-foreground/45',
        'hover:text-foreground/75 focus-visible:outline-none',
        active && 'text-foreground',
      )}
    >
      {children}
      {active && (
        <span
          aria-hidden
          className="absolute inset-x-0 bottom-0 h-0.5 rounded-full bg-accent"
        />
      )}
    </button>
  );
}

export function DeleteListDialog({
  open,
  label,
  onOpenChange,
  onDelete,
}: {
  open: boolean;
  label: string;
  onOpenChange: (open: boolean) => void;
  onDelete: () => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete “{label}”?</DialogTitle>
          <DialogDescription>
            The server does not expose permanent list deletion yet, so this removes
            the list from this device. Objects in the list are not deleted.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="ghost">
              Cancel
            </Button>
          </DialogClose>
          <Button
            type="button"
            variant="danger"
            onClick={() => {
              onDelete();
              onOpenChange(false);
            }}
          >
            Delete
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
