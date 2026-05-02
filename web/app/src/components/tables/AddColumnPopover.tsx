import { useEffect, useMemo, useState } from 'react';
import { Plus, Search } from 'lucide-react';
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
  PopoverClose,
} from '@/components/ui/Popover';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Label } from '@/components/ui/Label';
import { toast } from '@/components/ui/Toast';
import {
  uiKind as toUIKind,
  useAddPropertyToType,
  useAllPropertiesInSpace,
  toAddPropertyParts,
  type PropertyDefWithOwner,
  type UIPropertyKind,
} from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';

interface Props {
  spaceId: string;
  typeId: string;
}

const KIND_OPTIONS: { value: UIPropertyKind; label: string }[] = [
  { value: 'string', label: 'Text' },
  { value: 'longtext', label: 'Long text' },
  { value: 'number', label: 'Number' },
  { value: 'boolean', label: 'Yes / No' },
  { value: 'date', label: 'Date' },
  { value: 'url', label: 'URL' },
  { value: 'email', label: 'Email' },
  { value: 'tags', label: 'Tags' },
  { value: 'relation', label: 'Relation → object' },
];

const UI_KIND_LABEL: Record<UIPropertyKind, string> = {
  string: 'Text',
  longtext: 'Long text',
  number: 'Number',
  boolean: 'Yes / No',
  date: 'Date',
  url: 'URL',
  email: 'Email',
  tags: 'Tags',
  relation: 'Relation',
  array: 'Array',
  object: 'Object',
  null: 'Null',
};

type Tab = 'new' | 'reuse';

/**
 * Inline column-add affordance — header `+` button opens a popover
 * with two tabs:
 *   - "New":  name + kind, fires `addPropertyToType`.
 *   - "From another list": searchable list of every user property
 *     defined elsewhere in this space; picking one fires the same
 *     mutation with that property's (name, kind, xKey) — see
 *     docs/specs/PR-022-reuse-property-shape.md.
 */
export function AddColumnPopover({ spaceId, typeId }: Props) {
  const [open, setOpen] = useState(false);
  const [tab, setTab] = useState<Tab>('new');

  // Reset to "new" each time the popover opens.
  useEffect(() => {
    if (open) setTab('new');
  }, [open]);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label="Add column"
          title="Add column"
          className={cn(
            'flex h-8 w-8 items-center justify-center text-foreground/50',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <Plus className="h-3.5 w-3.5" aria-hidden />
        </button>
      </PopoverTrigger>
      <PopoverContent align="end" sideOffset={2} className="w-80 p-0">
        <div role="tablist" className="flex border-b border-foreground/10 px-1 pt-1">
          <TabButton current={tab} value="new" onSelect={setTab}>
            New
          </TabButton>
          <TabButton current={tab} value="reuse" onSelect={setTab}>
            From another list
          </TabButton>
        </div>

        {tab === 'new' ? (
          <NewPropertyPanel
            spaceId={spaceId}
            typeId={typeId}
            onClose={() => setOpen(false)}
          />
        ) : (
          <ReusePropertyPanel
            spaceId={spaceId}
            currentTypeId={typeId}
            onClose={() => setOpen(false)}
          />
        )}
      </PopoverContent>
    </Popover>
  );
}

function TabButton({
  current,
  value,
  onSelect,
  children,
}: {
  current: Tab;
  value: Tab;
  onSelect: (t: Tab) => void;
  children: React.ReactNode;
}) {
  const active = current === value;
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={() => onSelect(value)}
      className={cn(
        'mx-0.5 rounded-md px-2.5 py-1 text-[12px]',
        active
          ? 'bg-foreground/[0.06] text-foreground'
          : 'text-foreground/55 hover:text-foreground/80',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      {children}
    </button>
  );
}

function NewPropertyPanel({
  spaceId,
  typeId,
  onClose,
}: {
  spaceId: string;
  typeId: string;
  onClose: () => void;
}) {
  const [name, setName] = useState('');
  const [uiKind, setUiKind] = useState<UIPropertyKind>('string');
  const addProp = useAddPropertyToType(spaceId);

  const trimmed = name.trim();
  const canSubmit = trimmed.length > 0 && !addProp.isPending;

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    try {
      const parts = toAddPropertyParts(uiKind);
      const req = parts.xKey
        ? { name: trimmed, kind: parts.kind, xKey: parts.xKey }
        : { name: trimmed, kind: parts.kind };
      await addProp.mutateAsync({ typeId, req });
      toast.success(`Added column “${trimmed}”`);
      setName('');
      setUiKind('string');
      onClose();
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add column';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <form onSubmit={(e) => void onSubmit(e)} className="space-y-3 p-3">
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="add-column-name">Name</Label>
        <Input
          id="add-column-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Servings"
          required
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="add-column-kind">Type</Label>
        <select
          id="add-column-kind"
          value={uiKind}
          onChange={(e) => setUiKind(e.target.value as UIPropertyKind)}
          className={cn(
            'h-8 rounded-md border border-foreground/15 bg-background px-2 text-sm',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          {KIND_OPTIONS.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </div>
      <div className="flex justify-end gap-2">
        <PopoverClose asChild>
          <Button type="button" variant="ghost" size="sm">
            Cancel
          </Button>
        </PopoverClose>
        <Button type="submit" size="sm" disabled={!canSubmit}>
          {addProp.isPending ? 'Adding…' : 'Add'}
        </Button>
      </div>
    </form>
  );
}

function ReusePropertyPanel({
  spaceId,
  currentTypeId,
  onClose,
}: {
  spaceId: string;
  currentTypeId: string;
  onClose: () => void;
}) {
  const [query, setQuery] = useState('');
  const { properties, isLoading } = useAllPropertiesInSpace(spaceId);
  const addProp = useAddPropertyToType(spaceId);

  const candidates = useMemo(() => {
    const q = query.trim().toLowerCase();
    return properties
      .filter((p) => p.ownerTypeId !== currentTypeId)
      .filter((p) => {
        if (!q) return true;
        return (
          (p.name ?? '').toLowerCase().includes(q) ||
          p.ownerTypeName.toLowerCase().includes(q)
        );
      });
  }, [properties, currentTypeId, query]);

  const pick = async (p: PropertyDefWithOwner) => {
    const name = (p.name ?? '').trim();
    if (!name) {
      toast.error('Source property has no name');
      return;
    }
    try {
      const req = p.xKey
        ? { name, kind: p.kind, xKey: p.xKey }
        : { name, kind: p.kind };
      await addProp.mutateAsync({ typeId: currentTypeId, req });
      toast.success(`Added column “${name}”`);
      onClose();
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add column';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-2 border-b border-foreground/10 px-2 py-1.5">
        <Search className="h-3.5 w-3.5 text-foreground/40" aria-hidden />
        <Input
          // eslint-disable-next-line jsx-a11y/no-autofocus -- Radix popover focus trap
          autoFocus
          type="text"
          autoComplete="off"
          placeholder="Search properties…"
          className="h-7 border-0 bg-transparent px-0 text-[13px] focus-visible:ring-0 focus-visible:ring-offset-0"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>
      <ul role="listbox" className="max-h-72 overflow-y-auto py-1">
        {isLoading && (
          <li className="px-3 py-2 text-[13px] text-foreground/40">Loading…</li>
        )}
        {!isLoading && candidates.length === 0 && (
          <li className="px-3 py-2 text-[13px] text-foreground/40">
            {query
              ? 'No matching properties.'
              : 'No properties defined in other lists yet.'}
          </li>
        )}
        {candidates.map((p) => (
          <li key={`${p.ownerTypeId}:${p.id}`} role="option" aria-selected={false}>
            <button
              type="button"
              onClick={() => void pick(p)}
              disabled={addProp.isPending}
              className={cn(
                'flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-[13px]',
                'hover:bg-foreground/[0.04]',
                'focus-visible:outline-none focus-visible:bg-foreground/[0.04]',
                'disabled:opacity-50',
              )}
            >
              <span className="min-w-0 truncate">
                <span className="text-foreground">{p.name || 'Unnamed'}</span>
                <span className="ml-2 text-foreground/40">
                  {p.ownerTypeName}
                </span>
              </span>
              <span className="shrink-0 text-[11px] uppercase tracking-wide text-foreground/40">
                {UI_KIND_LABEL[toUIKind(p)]}
              </span>
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
