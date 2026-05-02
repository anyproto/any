import { useState } from 'react';
import { Plus } from 'lucide-react';
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
  useAddPropertyToType,
  toAddPropertyParts,
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
];

/**
 * Inline column-add affordance — header `+` button opens a popover
 * with name + kind, fires `addPropertyToType`. Same one-shot flow as
 * the type wizard's properties step but with a richer kind list.
 *
 * UI sub-kinds (Long text / Date / URL / Email / Tags) are sent via
 * the property's xKey field per docs/specs/PR-013-property-types.md.
 */
export function AddColumnPopover({ spaceId, typeId }: Props) {
  const [open, setOpen] = useState(false);
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
      setOpen(false);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add column';
      toast.error(`${code}: ${msg}`);
    }
  };

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
      <PopoverContent align="end" sideOffset={2}>
        <form onSubmit={(e) => void onSubmit(e)} className="space-y-3">
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
      </PopoverContent>
    </Popover>
  );
}
