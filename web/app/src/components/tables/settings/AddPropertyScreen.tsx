import { useState } from 'react';
import {
  CalendarDays,
  Hash,
  Link,
  Mail,
  Search,
  Tags,
  Text,
  ToggleLeft,
  Type,
  type LucideIcon,
} from 'lucide-react';
import { Input, toast } from '@/components/ui';
import { ApiError } from '@/lib/api/client';
import {
  toAddPropertyParts,
  useAddPropertyToType,
  type UIPropertyKind,
} from '@/lib/api/types';
import { cn } from '@/lib/cn';

const PROPERTY_CREATE_OPTIONS: {
  kind: UIPropertyKind;
  label: string;
  icon: LucideIcon;
}[] = [
  { kind: 'string', label: 'Text', icon: Text },
  { kind: 'longtext', label: 'Long text', icon: Type },
  { kind: 'number', label: 'Number', icon: Hash },
  { kind: 'boolean', label: 'Checkbox', icon: ToggleLeft },
  { kind: 'date', label: 'Date', icon: CalendarDays },
  { kind: 'tags', label: 'Tags', icon: Tags },
  { kind: 'relation', label: 'Object', icon: Type },
  { kind: 'url', label: 'URL', icon: Link },
  { kind: 'email', label: 'Email', icon: Mail },
];

export function AddPropertyScreen({
  spaceId,
  typeId,
  onCreated,
}: {
  spaceId: string;
  typeId: string;
  onCreated: () => void;
}) {
  const [newPropertyName, setNewPropertyName] = useState('');
  const addProperty = useAddPropertyToType(spaceId);

  const createProperty = async (kind: UIPropertyKind, fallbackName: string) => {
    const trimmed = newPropertyName.trim();
    const name = trimmed || fallbackName;
    try {
      const parts = toAddPropertyParts(kind);
      const req = parts.xKey
        ? { name, kind: parts.kind, xKey: parts.xKey }
        : { name, kind: parts.kind };
      await addProperty.mutateAsync({ typeId, req });
      toast.success(`Added ${name}`);
      setNewPropertyName('');
      onCreated();
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add property';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <div className="space-y-4">
      <div className="relative">
        <Search
          className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground/40"
          aria-hidden
        />
        <Input
          value={newPropertyName}
          onChange={(e) => setNewPropertyName(e.target.value)}
          placeholder="Search or create property"
          className="h-9 pl-7 text-sm"
          aria-label="Property name"
        />
      </div>

      <section>
        <p className="mb-2 px-2 text-xs font-medium text-foreground/45">Create</p>
        <div className="space-y-1">
          {PROPERTY_CREATE_OPTIONS.map((option) => (
            <CreatePropertyOption
              key={option.kind}
              label={option.label}
              icon={option.icon}
              disabled={addProperty.isPending}
              onClick={() => void createProperty(option.kind, option.label)}
            />
          ))}
        </div>
      </section>
    </div>
  );
}

function CreatePropertyOption({
  label,
  icon: Icon,
  disabled,
  onClick,
}: {
  label: string;
  icon: LucideIcon;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className={cn(
        'flex h-8 w-full items-center gap-2 rounded-md px-2 text-sm text-foreground/80',
        'hover:bg-foreground/[0.04]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        'disabled:cursor-wait disabled:opacity-60',
      )}
    >
      <Icon className="h-3.5 w-3.5 shrink-0 text-foreground/45" aria-hidden />
      <span className="min-w-0 truncate">{label}</span>
    </button>
  );
}
