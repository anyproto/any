import {
  forwardRef,
  type ButtonHTMLAttributes,
} from 'react';
import {
  ArrowLeft,
  Check,
  ChevronRight,
  Trash2,
  Type,
  WrapText,
  X,
  type LucideIcon,
} from 'lucide-react';
import { Input } from '@/components/ui';
import { uiKind, type PropertyDef, type UIPropertyKind } from '@/lib/api/types';
import { cn } from '@/lib/cn';

export function PanelHeader({
  title,
  subtitle,
  onBack,
  onClose,
}: {
  title: string;
  subtitle?: string | undefined;
  onBack?: (() => void) | undefined;
  onClose: () => void;
}) {
  return (
    <div className="mb-5 flex items-start justify-between gap-3">
      <div className="flex min-w-0 items-start gap-2">
        {onBack && (
          <button
            type="button"
            aria-label="Back to view settings"
            onClick={onBack}
            className={cn(
              'mt-[-0.25rem] inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-foreground/45',
              'hover:bg-foreground/5 hover:text-foreground',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <ArrowLeft className="h-4 w-4" aria-hidden />
          </button>
        )}
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold text-foreground">{title}</p>
          {subtitle && <p className="text-xs text-foreground/45">{subtitle}</p>}
        </div>
      </div>
      <button
        type="button"
        aria-label="Close view settings"
        onClick={onClose}
        className={cn(
          'mt-[-0.25rem] inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md text-foreground/45',
          'hover:bg-foreground/5 hover:text-foreground',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <X className="h-4 w-4" aria-hidden />
      </button>
    </div>
  );
}

export function propertyKindLabel(kind: UIPropertyKind) {
  switch (kind) {
    case 'string':
      return 'Text';
    case 'longtext':
      return 'Long text';
    case 'number':
      return 'Number';
    case 'boolean':
      return 'Checkbox';
    case 'date':
      return 'Date';
    case 'url':
      return 'URL';
    case 'email':
      return 'Email';
    case 'tags':
      return 'Tags';
    case 'relation':
      return 'Object';
    case 'array':
      return 'Array';
    case 'object':
      return 'Object';
    case 'null':
      return 'Empty';
  }
}

export function propertyDisplayName(prop: PropertyDef) {
  return prop.name ?? `Untitled (${prop.id.slice(0, 6)}...)`;
}

export function PropertyListRow({
  name,
  kindLabel,
  fixed,
  onClick,
}: {
  name: string;
  kindLabel: string;
  fixed?: boolean;
  onClick?: () => void;
}) {
  const content = (
    <>
      <span className="h-3.5 w-3.5 shrink-0 text-foreground/30" aria-hidden />
      <span className="min-w-0 flex-1 truncate text-left">{name}</span>
      <span className="shrink-0 text-xs text-foreground/45">{kindLabel}</span>
      {!fixed && (
        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-foreground/35" aria-hidden />
      )}
    </>
  );

  if (fixed) {
    return (
      <div className="flex h-8 items-center gap-2 rounded-md px-2 text-sm text-foreground/55">
        {content}
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex h-8 w-full items-center gap-2 rounded-md px-2 text-sm text-foreground/80',
        'hover:bg-foreground/[0.04]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      {content}
    </button>
  );
}

export function EditPropertyScreen({ prop }: { prop: PropertyDef }) {
  const kind = uiKind(prop);
  const name = propertyDisplayName(prop);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <div className="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-foreground/[0.05] text-foreground/45">
          <Type className="h-4 w-4" aria-hidden />
        </div>
        <Input
          aria-label="Property name"
          value={name}
          readOnly
          className="h-9 bg-foreground/[0.04]"
        />
      </div>

      <div className="space-y-1">
        <SettingRow label="Type" value={propertyKindLabel(kind)} />
        <SettingRow label="Scope" value="Global" />
      </div>

      <div className="h-px bg-foreground/[0.08]" />

      <div className="space-y-1">
        <SettingRowIcon icon={WrapText} label="Wrap content" />
        <SettingRowIcon icon={Trash2} label="Delete from collection" />
      </div>

      <div className="h-px bg-foreground/[0.08]" />

      <section>
        <p className="mb-2 px-2 text-xs font-medium text-foreground/45">Collections</p>
        <p className="px-2 text-sm text-foreground/55">
          This property is attached to this list.
        </p>
      </section>

      {kind === 'tags' && (
        <section>
          <p className="mb-2 px-2 text-xs font-medium text-foreground/45">Options</p>
          <p className="px-2 text-sm text-foreground/55">
            Tag options are not editable in the current API.
          </p>
        </section>
      )}
    </div>
  );
}

export function SettingRowIcon({ icon: Icon, label }: { icon: LucideIcon; label: string }) {
  return (
    <div className="flex h-8 items-center gap-2 rounded-md px-2 text-sm text-foreground/55">
      <Icon className="h-3.5 w-3.5 shrink-0 text-foreground/40" aria-hidden />
      <span className="min-w-0 truncate">{label}</span>
    </div>
  );
}

export function SortOption({
  label,
  active,
  onClick,
}: {
  label: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex h-9 w-full items-center justify-between gap-3 rounded-md px-2 text-sm',
        'text-foreground hover:bg-foreground/[0.04]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      <span className="min-w-0 truncate">{label}</span>
      {active && <Check className="h-3.5 w-3.5 shrink-0 text-accent" aria-hidden />}
    </button>
  );
}

export function LayoutOption({
  icon: Icon,
  label,
  description,
  active,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  description: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'flex w-full items-center gap-3 rounded-[10px] px-3 py-3 text-left',
        'text-foreground hover:bg-foreground/[0.04]',
        active && 'bg-accent/[0.08] ring-1 ring-inset ring-accent/25',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      <span
        className={cn(
          'inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[9px] bg-foreground/[0.055] text-foreground/45',
          active && 'bg-accent/15 text-accent',
        )}
      >
        <Icon className="h-4 w-4" aria-hidden />
      </span>
      <span className="min-w-0 flex-1">
        <span className="block text-sm font-medium">{label}</span>
        <span className="mt-0.5 block text-xs text-foreground/45">{description}</span>
      </span>
      {active && <Check className="h-3.5 w-3.5 shrink-0 text-accent" aria-hidden />}
    </button>
  );
}

export function PlaceholderScreen({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded-md border border-foreground/[0.08] px-3 py-3">
      <p className="text-sm font-medium text-foreground">{title}</p>
      <p className="mt-1 text-sm text-foreground/50">{body}</p>
    </div>
  );
}

export function SettingRow({ label, value }: { label: string; value?: string }) {
  return (
    <div className="flex h-9 items-center justify-between gap-3 rounded-md px-2 text-sm text-foreground/45">
      <span className="min-w-0 truncate">{label}</span>
      {value && <span className="shrink-0 text-foreground/40">{value}</span>}
    </div>
  );
}

export const SettingRowButton = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement> & {
    label: string;
    value?: string;
  }
>(function SettingRowButton({ label, value, className, type = 'button', ...props }, ref) {
  return (
    <button
      ref={ref}
      type={type}
      className={cn(
        'flex h-9 w-full items-center justify-between gap-3 rounded-md px-2 text-sm',
        'text-foreground hover:bg-foreground/[0.04]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        className,
      )}
      {...props}
    >
      <span className="min-w-0 truncate">{label}</span>
      <span className="flex min-w-0 shrink items-center gap-1 text-foreground/45">
        {value && <span className="max-w-32 truncate">{value}</span>}
        <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden />
      </span>
    </button>
  );
});
