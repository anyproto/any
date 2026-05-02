import { useMemo, useState } from 'react';
import { Sparkles } from 'lucide-react';
import {
  useObject,
  useSetObjectProperty,
  type ObjectRecord,
} from '@/lib/api/objects';
import {
  useType,
  useTypeProperties,
  uiKind,
  type PropertyDef,
  type UIPropertyKind,
} from '@/lib/api/types';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui/Toast';
import { TextCell } from '@/components/tables/cells/TextCell';
import { NumberCell } from '@/components/tables/cells/NumberCell';
import { BoolCell } from '@/components/tables/cells/BoolCell';
import { LongTextCell } from '@/components/tables/cells/LongTextCell';
import { DateCell } from '@/components/tables/cells/DateCell';
import { UrlCell } from '@/components/tables/cells/UrlCell';
import { EmailCell } from '@/components/tables/cells/EmailCell';
import { TagsCell } from '@/components/tables/cells/TagsCell';
import { RelationCell } from '@/components/tables/cells/RelationCell';
import { cn } from '@/lib/cn';

interface Props {
  spaceId: string;
  objectId: string;
}

/**
 * Below the page title — one chip per type the object is stamped
 * with. Click a chip to expand its property values inline. See
 * docs/specs/PR-020-object-type-bar.md for v1 scope.
 */
export function ObjectTypeBar({ spaceId, objectId }: Props) {
  const objQuery = useObject(spaceId, objectId);
  const [expanded, setExpanded] = useState<string | null>(null);

  const types = useMemo(() => {
    const arr = objQuery.data?.any?.types ?? [];
    // Drop the implicit `nav` namespace if it ever leaks into
    // any.types — it's not a user-facing list.
    return arr.filter((t) => t && t !== 'nav');
  }, [objQuery.data]);

  if (types.length === 0) return null;

  return (
    <div className="mb-4 mt-1">
      <div className="flex flex-wrap items-center gap-1.5">
        {types.map((typeId) => (
          <TypeChip
            key={typeId}
            spaceId={spaceId}
            typeId={typeId}
            active={expanded === typeId}
            onToggle={() =>
              setExpanded((cur) => (cur === typeId ? null : typeId))
            }
          />
        ))}
      </div>

      {expanded && objQuery.data && (
        <TypePropertiesPanel
          spaceId={spaceId}
          typeId={expanded}
          row={objQuery.data}
        />
      )}
    </div>
  );
}

function TypeChip({
  spaceId,
  typeId,
  active,
  onToggle,
}: {
  spaceId: string;
  typeId: string;
  active: boolean;
  onToggle: () => void;
}) {
  const typeQuery = useType(spaceId, typeId);
  const label = typeQuery.data?.name?.trim() || `List ${typeId.slice(0, 6)}…`;

  return (
    <button
      type="button"
      onClick={onToggle}
      aria-pressed={active}
      className={cn(
        'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[13px]',
        'transition-colors',
        active
          ? 'border-foreground/15 bg-foreground/[0.06] text-foreground'
          : 'border-foreground/10 bg-transparent text-foreground/70 hover:bg-foreground/[0.04]',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      <Sparkles className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
      <span className="truncate">{label}</span>
    </button>
  );
}

function TypePropertiesPanel({
  spaceId,
  typeId,
  row,
}: {
  spaceId: string;
  typeId: string;
  row: ObjectRecord;
}) {
  const propsQuery = useTypeProperties(spaceId, typeId);
  const setProp = useSetObjectProperty(spaceId);

  // Skip `name` (it's not a type prop anyway since it lives in
  // `any`, but be defensive in case a type ever defines one).
  const visibleProps = (propsQuery.data ?? []).filter(
    (p) => p.id !== 'name' && (p.name ?? '').trim() !== '',
  );

  const onCommit = async (propId: string, value: unknown) => {
    try {
      await setProp.mutateAsync({
        objectId: row.id,
        typeId,
        patch: { [propId]: value },
      });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to save';
      toast.error(`${code}: ${msg}`);
    }
  };

  if (propsQuery.isPending) {
    return (
      <div className="mt-3 px-1 text-[13px] text-foreground/40">Loading…</div>
    );
  }

  if (visibleProps.length === 0) {
    return (
      <div className="mt-3 px-1 text-[13px] text-foreground/40">
        This list has no properties yet.
      </div>
    );
  }

  return (
    <dl className="mt-3 grid grid-cols-[140px_1fr] items-center gap-x-3 gap-y-0.5">
      {visibleProps.map((p) => {
        const propLabel = p.name?.trim() || `Property ${p.id.slice(0, 6)}…`;
        const value = readPropValue(row, typeId, p.id);
        return (
          <div className="contents" key={p.id}>
            <dt className="truncate text-[13px] text-foreground/55">
              {propLabel}
            </dt>
            <dd className="min-w-0">
              <PropertyValue
                prop={p}
                rowId={row.id}
                value={value}
                onCommit={(v) => onCommit(p.id, v)}
              />
            </dd>
          </div>
        );
      })}
    </dl>
  );
}

function PropertyValue({
  prop,
  rowId,
  value,
  onCommit,
}: {
  prop: PropertyDef;
  rowId: string;
  value: unknown;
  onCommit: (v: unknown) => void | Promise<void>;
}) {
  const k: UIPropertyKind = uiKind(prop);
  switch (k) {
    case 'string':
      return (
        <TextCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'longtext':
      return (
        <LongTextCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'number':
      return (
        <NumberCell
          value={typeof value === 'number' ? value : null}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'boolean':
      return (
        <BoolCell
          value={typeof value === 'boolean' ? value : null}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'date':
      return (
        <DateCell
          value={typeof value === 'string' ? value : null}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'url':
      return (
        <UrlCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'email':
      return (
        <EmailCell
          value={typeof value === 'string' ? value : ''}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'tags':
      return (
        <TagsCell
          value={Array.isArray(value) ? (value as string[]) : []}
          onCommit={(v) => onCommit(v)}
        />
      );
    case 'relation':
      return (
        <RelationCell
          value={typeof value === 'string' ? value : ''}
          rowId={rowId}
          onCommit={(v) => onCommit(v)}
        />
      );
    default:
      return (
        <span className="text-[12px] text-foreground/40">
          <code className="font-mono">
            {value === undefined ? '—' : JSON.stringify(value)}
          </code>
        </span>
      );
  }
}

function readPropValue(row: ObjectRecord, typeId: string, propId: string): unknown {
  const ns = (row as Record<string, unknown>)[typeId];
  if (ns && typeof ns === 'object' && !Array.isArray(ns)) {
    return (ns as Record<string, unknown>)[propId];
  }
  return undefined;
}
