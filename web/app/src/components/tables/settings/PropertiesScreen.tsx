import { Plus } from 'lucide-react';
import type { PropertyDef } from '@/lib/api/types';
import { uiKind } from '@/lib/api/types';
import { cn } from '@/lib/cn';
import {
  PropertyListRow,
  propertyDisplayName,
  propertyKindLabel,
} from './settingsPrimitives';

export function PropertiesScreen({
  props,
  onAddProperty,
  onSelectProperty,
}: {
  props: PropertyDef[];
  onAddProperty: () => void;
  onSelectProperty: (propId: string) => void;
}) {
  return (
    <div className="space-y-2">
      <PropertyListRow name="Name" kindLabel="Title" fixed />
      {props.length === 0 ? (
        <p className="px-2 py-1.5 text-sm text-foreground/45">No properties yet</p>
      ) : (
        props.map((p) => (
          <PropertyListRow
            key={p.id}
            name={propertyDisplayName(p)}
            kindLabel={propertyKindLabel(uiKind(p))}
            onClick={() => onSelectProperty(p.id)}
          />
        ))
      )}
      <button
        type="button"
        onClick={onAddProperty}
        className={cn(
          'mt-2 flex h-8 w-full items-center gap-2 rounded-md px-2 text-sm text-foreground/60',
          'hover:bg-foreground/5 hover:text-foreground',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <Plus className="h-3.5 w-3.5" aria-hidden />
        Add
      </button>
    </div>
  );
}
