import {
  forwardRef,
  type ButtonHTMLAttributes,
  type ReactNode,
} from 'react';
import {
  ArrowDownAZ,
  ArrowUpAZ,
  Filter,
  List as ListLayoutIcon,
  Plus,
  Search,
  SlidersHorizontal,
  Table2,
  X,
} from 'lucide-react';
import type { TableViewLayout } from '@/atoms';
import type { PropertyDef } from '@/lib/api/types';
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
} from '@/components/ui';
import { cn } from '@/lib/cn';
import { sortLabel, type SortDir } from './tableSorting';

export function TableToolbar({
  typeId,
  props,
  layout,
  filter,
  filterOpen,
  hiddenPropCount,
  sortKey,
  sortDir,
  settingsOpen,
  createPending,
  onLayoutChange,
  onFilterChange,
  onFilterOpenChange,
  onSort,
  onSettingsOpenChange,
  onCreate,
}: {
  typeId: string;
  props: PropertyDef[];
  layout: TableViewLayout;
  filter: string;
  filterOpen: boolean;
  hiddenPropCount: number;
  sortKey: string;
  sortDir: SortDir;
  settingsOpen: boolean;
  createPending: boolean;
  onLayoutChange: (layout: TableViewLayout) => void;
  onFilterChange: (filter: string) => void;
  onFilterOpenChange: (open: boolean) => void;
  onSort: (key: string, dir: SortDir) => void;
  onSettingsOpenChange: (open: boolean) => void;
  onCreate: () => void;
}) {
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <button
          type="button"
          aria-current="page"
          className={cn(
            'relative h-8 px-0.5 text-[15px] font-semibold text-foreground',
            'after:absolute after:bottom-0 after:left-0 after:h-0.5 after:w-full after:rounded-full after:bg-foreground/80',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          All
        </button>
        <div className="flex flex-wrap items-center justify-end gap-1">
          <ViewLayoutSwitch layout={layout} onChange={onLayoutChange} />
          <ToolbarButton
            active={filterOpen || filter.trim() !== ''}
            onClick={() => onFilterOpenChange(!filterOpen)}
          >
            <Filter className="h-3.5 w-3.5" aria-hidden />
            Filter
          </ToolbarButton>
          <SortMenu
            props={props}
            typeId={typeId}
            sortKey={sortKey}
            sortDir={sortDir}
            onSort={onSort}
          />
          <ToolbarButton
            active={settingsOpen}
            onClick={() => onSettingsOpenChange(!settingsOpen)}
            aria-label="View settings"
          >
            <SlidersHorizontal className="h-3.5 w-3.5" aria-hidden />
            Settings
          </ToolbarButton>
          <Button
            size="sm"
            onClick={onCreate}
            disabled={createPending}
            aria-label="New row"
            className="ml-2 h-8 rounded-[10px] px-3 text-sm shadow-sm shadow-accent/10"
          >
            <Plus className="h-3.5 w-3.5" aria-hidden />
            {createPending ? 'Adding...' : 'New'}
          </Button>
        </div>
      </div>

      {(filterOpen || filter.trim() !== '') && (
        <div className="relative max-w-sm">
          <Search
            className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground/35"
            aria-hidden
          />
          <Input
            type="text"
            value={filter}
            onChange={(e) => onFilterChange(e.target.value)}
            placeholder="Filter by name..."
            className="h-9 rounded-[10px] border-0 bg-foreground/[0.055] pl-8 pr-8 text-sm shadow-none focus-visible:ring-1"
            aria-label="Filter rows"
          />
          {filter && (
            <button
              type="button"
              aria-label="Clear filter"
              onClick={() => onFilterChange('')}
              className={cn(
                'absolute right-1 top-1/2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded-md text-foreground/40',
                'hover:bg-foreground/5 hover:text-foreground',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
              )}
            >
              <X className="h-3.5 w-3.5" aria-hidden />
            </button>
          )}
        </div>
      )}
      {hiddenPropCount > 0 && (
        <p className="text-xs text-foreground/45">
          {hiddenPropCount} hidden {hiddenPropCount === 1 ? 'property' : 'properties'}
        </p>
      )}
    </>
  );
}

export const ToolbarButton = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement> & { active?: boolean }
>(function ToolbarButton({ active, className, type = 'button', ...props }, ref) {
  return (
    <button
      ref={ref}
      type={type}
      className={cn(
        'inline-flex h-8 items-center gap-1.5 rounded-[10px] px-2.5 text-sm font-medium',
        'text-foreground/55 hover:bg-foreground/[0.055] hover:text-foreground/85',
        active && 'bg-foreground/[0.075] text-foreground',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        className,
      )}
      {...props}
    />
  );
});

function ViewLayoutSwitch({
  layout,
  onChange,
}: {
  layout: TableViewLayout;
  onChange: (layout: TableViewLayout) => void;
}) {
  return (
    <div
      role="group"
      aria-label="View layout"
      className="mr-1 inline-flex h-8 items-center rounded-[10px] bg-foreground/[0.045] p-0.5"
    >
      <ViewLayoutButton
        label="Table layout"
        title="Table"
        active={layout === 'table'}
        onClick={() => onChange('table')}
      >
        <Table2 className="h-3.5 w-3.5" aria-hidden />
      </ViewLayoutButton>
      <ViewLayoutButton
        label="List layout"
        title="List"
        active={layout === 'list'}
        onClick={() => onChange('list')}
      >
        <ListLayoutIcon className="h-3.5 w-3.5" aria-hidden />
      </ViewLayoutButton>
    </div>
  );
}

function ViewLayoutButton({
  label,
  title,
  active,
  onClick,
  children,
}: {
  label: string;
  title: string;
  active: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      aria-pressed={active}
      title={title}
      onClick={onClick}
      className={cn(
        'inline-flex h-7 w-7 items-center justify-center rounded-[8px] text-foreground/50',
        'hover:bg-background/80 hover:text-foreground/80',
        active && 'bg-background text-foreground shadow-sm shadow-foreground/5',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      {children}
    </button>
  );
}

function SortMenu({
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
  const active = sortKey !== 'nav.pos' || sortDir !== 'asc';
  const currentLabel = sortLabel(props, typeId, sortKey);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <ToolbarButton active={active}>
          {sortDir === 'desc' ? (
            <ArrowDownAZ className="h-3.5 w-3.5" aria-hidden />
          ) : (
            <ArrowUpAZ className="h-3.5 w-3.5" aria-hidden />
          )}
          Sort
          {active && (
            <span className="max-w-24 truncate text-foreground/45">{currentLabel}</span>
          )}
        </ToolbarButton>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        <DropdownMenuLabel>Sort by</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => onSort('nav.pos', 'asc')}>
          Default order
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onSort('any.name', 'asc')}>Name</DropdownMenuItem>
        {props.map((p) => (
          <DropdownMenuItem key={p.id} onSelect={() => onSort(`${typeId}.${p.id}`, 'asc')}>
            {p.name ?? `Untitled (${p.id.slice(0, 6)}...)`}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Direction</DropdownMenuLabel>
        <DropdownMenuItem onSelect={() => onSort(sortKey, 'asc')}>
          Ascending
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onSort(sortKey, 'desc')}>
          Descending
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
