import type { PropertyDef } from '@/lib/api/types';
import type { SortDir } from '../tableSorting';
import { SortOption, propertyDisplayName } from './settingsPrimitives';

export function SortScreen({
  props,
  typeId,
  sortKey,
  sortDir,
  onSort,
}: {
  props: PropertyDef[];
  typeId: string;
  sortKey: string;
  sortDir: SortDir;
  onSort: (key: string, dir: SortDir) => void;
}) {
  return (
    <div className="space-y-5">
      <section>
        <p className="mb-2 px-2 text-xs font-medium text-foreground/45">Sort by</p>
        <div className="space-y-1">
          <SortOption
            label="Default order"
            active={sortKey === 'nav.pos'}
            onClick={() => onSort('nav.pos', 'asc')}
          />
          <SortOption
            label="Name"
            active={sortKey === 'any.name'}
            onClick={() => onSort('any.name', sortDir ?? 'asc')}
          />
          {props.map((p) => (
            <SortOption
              key={p.id}
              label={propertyDisplayName(p)}
              active={sortKey === `${typeId}.${p.id}`}
              onClick={() => onSort(`${typeId}.${p.id}`, sortDir ?? 'asc')}
            />
          ))}
        </div>
      </section>
      <section>
        <p className="mb-2 px-2 text-xs font-medium text-foreground/45">Direction</p>
        <div className="space-y-1">
          <SortOption
            label="Ascending"
            active={sortDir === 'asc'}
            onClick={() => onSort(sortKey, 'asc')}
          />
          <SortOption
            label="Descending"
            active={sortDir === 'desc'}
            onClick={() => onSort(sortKey, 'desc')}
          />
        </div>
      </section>
    </div>
  );
}
