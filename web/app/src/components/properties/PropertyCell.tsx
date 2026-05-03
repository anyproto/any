import { uiKind, type PropertyDef, type UIPropertyKind } from '@/lib/api/types';
import { assertNever } from '@/shared';
import { TextCell } from './cells/TextCell';
import { NumberCell } from './cells/NumberCell';
import { BoolCell } from './cells/BoolCell';
import { LongTextCell } from './cells/LongTextCell';
import { DateCell } from './cells/DateCell';
import { UrlCell } from './cells/UrlCell';
import { EmailCell } from './cells/EmailCell';
import { TagsCell } from './cells/TagsCell';
import { RelationCell } from './cells/RelationCell';

export function PropertyCell({
  prop,
  value,
  rowId,
  onCommit,
}: {
  prop: PropertyDef;
  value: unknown;
  rowId: string;
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
          value={stringArrayValue(value)}
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
    case 'array':
    case 'object':
    case 'null':
      return jsonFallback(value);
    default:
      return assertNever(k, 'Unhandled property UI kind');
  }
}

function stringArrayValue(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === 'string')
    : [];
}

function jsonFallback(value: unknown) {
  return (
    <span className="block px-2 py-1 text-[12px] text-foreground/50">
      <code className="font-mono">
        {value === undefined ? '—' : JSON.stringify(value)}
      </code>
    </span>
  );
}
