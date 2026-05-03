import {
  forwardRef,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type CSSProperties,
  type FormEvent,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from 'react';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import {
  ArrowLeft,
  ArrowDownAZ,
  ArrowUpAZ,
  CalendarDays,
  Check,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  Eye,
  EyeOff,
  Hash,
  Filter,
  GripVertical,
  Link,
  List as ListLayoutIcon,
  Mail,
  Plus,
  Search,
  SlidersHorizontal,
  Table2,
  Tags,
  Text,
  ToggleLeft,
  Trash2,
  Type,
  WrapText,
  X,
} from 'lucide-react';
import { activeObjectIdAtom, activeSpaceIdAtom, useTypeMeta } from '@/atoms';
import {
  normalizeTableColumnWidth,
  tableColumnWidthKey,
  legacyTableColumnWidthKey,
  tableColumnWidthsAtom,
  tableViewLayoutsAtom,
  normalizeTableViewLayout,
  TABLE_ADD_COLUMN_WIDTH,
  TABLE_NAME_COLUMN_WIDTH,
  TABLE_PROPERTY_COLUMN_WIDTH,
  type TableViewLayout,
} from '@/atoms';
import {
  useCreateObject,
  useObjectsByTypeInfinite,
} from '@/lib/api/objects';
import {
  useType,
  useTypeProperties,
  toAddPropertyParts,
  uiKind,
  useAddPropertyToType,
  type PropertyDef,
  type PropertyKind,
  type UIPropertyKind,
} from '@/lib/api/types';
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Input,
  toast,
} from '@/components/ui';
import { ApiError } from '@/lib/api/client';
import {
  ListIcon,
  ListIconDialog,
} from '@/components/types';
import { cn } from '@/lib/cn';
import { useVirtualRows } from '@/shared';
import { AddColumnPopover } from './AddColumnPopover';
import { ListRowsView, LIST_ROW_HEIGHT } from './ListRowsView';
import {
  AddRow,
  DataRow,
  LoadMoreRow,
  SpacerRow,
  TABLE_ROW_HEIGHT,
} from './TableRows';

type SortDir = 'asc' | 'desc' | null;
type SettingsScreen =
  | 'main'
  | 'layout'
  | 'property-visibility'
  | 'filter'
  | 'sort'
  | 'default-template'
  | 'connected-templates'
  | 'properties'
  | 'add-property'
  | 'edit-property';
const TABLE_PAGE_SIZE = 100;
const PROPERTY_VISIBILITY_ROW_HEIGHT = 32;
const PROPERTY_VISIBILITY_ROW_GAP = 4;
const PROPERTY_VISIBILITY_ROW_STEP =
  PROPERTY_VISIBILITY_ROW_HEIGHT + PROPERTY_VISIBILITY_ROW_GAP;

const PROPERTY_CREATE_OPTIONS: {
  kind: UIPropertyKind;
  label: string;
  icon: typeof Text;
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

interface TableViewProps {
  typeId: string;
}

/**
 * Database-style view of a type — rows are objects stamped with the
 * type, columns are the type's properties (plus Name).
 */
export function TableView({ typeId }: TableViewProps) {
  const spaceId = useAtomValue(activeSpaceIdAtom);
  const typeQuery = useType(spaceId, typeId);
  const propsQuery = useTypeProperties(spaceId, typeId);
  const [filter, setFilter] = useState('');
  const [filterOpen, setFilterOpen] = useState(false);
  const [debouncedFilter, setDebouncedFilter] = useState('');
  const [sortKey, setSortKey] = useState<string>('nav.pos');
  const [sortDir, setSortDir] = useState<SortDir>('asc');
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [hiddenPropIds, setHiddenPropIds] = useState<Set<string>>(() => new Set());
  const [draggedColumnPropId, setDraggedColumnPropId] = useState<string | null>(null);
  const [columnDropTargetPropId, setColumnDropTargetPropId] = useState<string | null>(
    null,
  );
  const [columnWidths, setColumnWidths] = useAtom(tableColumnWidthsAtom);
  const [viewLayouts, setViewLayouts] = useAtom(tableViewLayoutsAtom);
  const viewLayout = normalizeTableViewLayout(viewLayouts[typeId]);
  const setViewLayout = useCallback(
    (layout: TableViewLayout) => {
      setViewLayouts((prev) => {
        if (prev[typeId] === layout) return prev;
        return { ...prev, [typeId]: layout };
      });
    },
    [setViewLayouts, typeId],
  );

  // Debounce filter (200ms) so we don't refetch on every keystroke.
  useEffect(() => {
    const t = window.setTimeout(() => setDebouncedFilter(filter), 200);
    return () => window.clearTimeout(t);
  }, [filter]);

  const objectsQuery = useObjectsByTypeInfinite(spaceId, typeId, {
    sortKey,
    sortDir,
    pageSize: TABLE_PAGE_SIZE,
  });
  const rows = useMemo(
    () => objectsQuery.data?.pages.flatMap((page) => page.records) ?? [],
    [objectsQuery.data],
  );

  // Filter rows client-side by name (server doesn't expose a $regex
  // we can rely on across SDK versions; flagged in spec).
  const visibleRows = useMemo(() => {
    const q = debouncedFilter.trim().toLowerCase();
    if (q === '') return rows;
    return rows.filter((r) => (r.any?.name ?? '').toLowerCase().includes(q));
  }, [debouncedFilter, rows]);

  const virtual = useVirtualRows({
    count: visibleRows.length,
    rowHeight: viewLayout === 'list' ? LIST_ROW_HEIGHT : TABLE_ROW_HEIGHT,
    overscan: 10,
  });
  const virtualRows = visibleRows.slice(virtual.startIndex, virtual.endIndex);
  const { fetchNextPage, hasNextPage, isFetchingNextPage } = objectsQuery;

  useEffect(() => {
    if (debouncedFilter !== '') return;
    if (!hasNextPage || isFetchingNextPage) return;
    if (virtual.endIndex >= Math.max(0, visibleRows.length - 20)) {
      void fetchNextPage();
    }
  }, [
    debouncedFilter,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    virtual.endIndex,
    visibleRows.length,
  ]);

  const rawUserProps = useMemo(
    () => (propsQuery.data ?? []).filter((p) => isUserKind(p.kind)),
    [propsQuery.data],
  );
  const rawUserPropIds = useMemo(() => rawUserProps.map((p) => p.id), [rawUserProps]);
  const rawUserPropIdsKey = rawUserPropIds.join('\u0000');
  const [propertyOrder, setPropertyOrder] = useState<string[]>([]);

  useEffect(() => {
    setPropertyOrder((prev) => {
      const next = prev.filter((id) => rawUserPropIds.includes(id));
      for (const id of rawUserPropIds) {
        if (!next.includes(id)) next.push(id);
      }
      if (next.length === prev.length && next.every((id, index) => id === prev[index]))
        return prev;
      return next;
    });
  }, [rawUserPropIds, rawUserPropIdsKey]);

  const userProps = useMemo(() => {
    if (propertyOrder.length === 0) return rawUserProps;
    const byId = new Map(rawUserProps.map((p) => [p.id, p]));
    const ordered = propertyOrder
      .map((id) => byId.get(id))
      .filter((p): p is PropertyDef => p != null);
    const orderedIds = new Set(ordered.map((p) => p.id));
    return [...ordered, ...rawUserProps.filter((p) => !orderedIds.has(p.id))];
  }, [propertyOrder, rawUserProps]);
  const visibleProps = userProps.filter((p) => !hiddenPropIds.has(p.id));
  const columnWidth = useCallback(
    (columnId: string, fallback: number) => {
      const width =
        columnWidths[tableColumnWidthKey(typeId, columnId)] ??
        columnWidths[legacyTableColumnWidthKey(typeId, columnId)] ??
        fallback;
      return normalizeTableColumnWidth(
        width,
        fallback,
      );
    },
    [columnWidths, typeId],
  );
  const nameColumnWidth = columnWidth('name', TABLE_NAME_COLUMN_WIDTH);
  const propertyColumnWidths = useMemo(() => {
    const widths: Record<string, number> = {};
    for (const prop of visibleProps) {
      widths[prop.id] = columnWidth(prop.id, TABLE_PROPERTY_COLUMN_WIDTH);
    }
    return widths;
  }, [columnWidth, visibleProps]);
  const tablePixelWidth = useMemo(
    () =>
      nameColumnWidth +
      visibleProps.reduce(
        (sum, prop) => sum + (propertyColumnWidths[prop.id] ?? TABLE_PROPERTY_COLUMN_WIDTH),
        0,
      ) +
      TABLE_ADD_COLUMN_WIDTH,
    [nameColumnWidth, propertyColumnWidths, visibleProps],
  );
  const resizeColumn = useCallback(
    (columnId: string, width: number, fallback: number) => {
      const normalized = normalizeTableColumnWidth(width, fallback);
      setColumnWidths((prev) => {
        const key = tableColumnWidthKey(typeId, columnId);
        const legacyKey = legacyTableColumnWidthKey(typeId, columnId);
        if (prev[key] === normalized) return prev;
        const next = { ...prev, [key]: normalized };
        delete next[legacyKey];
        return next;
      });
    },
    [setColumnWidths, typeId],
  );
  const rawTypeName =
    typeQuery.data?.name?.trim() || `Untitled (${typeId.slice(0, 6)}…)`;
  const {
    overrideName: overrideTypeName,
    overrideIcon: overrideTypeIcon,
    set: setTypeMeta,
  } = useTypeMeta(spaceId, typeId);
  const typeName = overrideTypeName?.trim() || rawTypeName;
  const create = useCreateObject(spaceId);
  const setActiveObjectId = useSetAtom(activeObjectIdAtom);

  const toggleProperty = (propId: string, visible: boolean) => {
    setHiddenPropIds((prev) => {
      const next = new Set(prev);
      if (visible) next.delete(propId);
      else next.add(propId);
      return next;
    });
  };

  const moveProperty = (sourceId: string, targetId: string) => {
    if (sourceId === targetId) return;
    setPropertyOrder((prev) => {
      const base = prev.length > 0 ? prev : rawUserPropIds;
      const next = base.filter((id) => rawUserPropIds.includes(id));
      for (const id of rawUserPropIds) {
        if (!next.includes(id)) next.push(id);
      }

      const from = next.indexOf(sourceId);
      const to = next.indexOf(targetId);
      if (from < 0 || to < 0) return prev;

      const [moved] = next.splice(from, 1);
      if (moved == null) return prev;
      next.splice(to, 0, moved);
      return next;
    });
  };

  const endColumnDrag = () => {
    setDraggedColumnPropId(null);
    setColumnDropTargetPropId(null);
  };

  if (!spaceId) return null;

  const createRow = async () => {
    try {
      const { objectId } = await create.mutateAsync({ typeIds: [typeId] });
      setActiveObjectId(objectId);
      toast.success(`Created ${typeName}`);
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add row';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <div className="flex h-full flex-col bg-background">
      <header className="shrink-0 px-8 pb-3 pt-8">
        <div className="mx-auto flex w-full max-w-[1180px] flex-col gap-6">
          <ListTitleEditor
            name={typeName}
            icon={overrideTypeIcon}
            onRename={(name) => setTypeMeta({ name })}
            onIconChange={(icon) => setTypeMeta({ icon })}
          />

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
              <ViewLayoutSwitch layout={viewLayout} onChange={setViewLayout} />
              <ToolbarButton
                active={filterOpen || filter.trim() !== ''}
                onClick={() => setFilterOpen((v) => !v)}
              >
                <Filter className="h-3.5 w-3.5" aria-hidden />
                Filter
              </ToolbarButton>
              <SortMenu
                props={userProps}
                typeId={typeId}
                sortKey={sortKey}
                sortDir={sortDir}
                onSort={(key, dir) => {
                  setSortKey(key);
                  setSortDir(dir);
                }}
              />
              <ToolbarButton
                active={settingsOpen}
                onClick={() => setSettingsOpen((v) => !v)}
                aria-label="View settings"
              >
                <SlidersHorizontal className="h-3.5 w-3.5" aria-hidden />
                Settings
              </ToolbarButton>
              <Button
                size="sm"
                onClick={() => void createRow()}
                disabled={create.isPending}
                aria-label="New row"
                className="ml-2 h-8 rounded-[10px] px-3 text-sm shadow-sm shadow-accent/10"
              >
                <Plus className="h-3.5 w-3.5" aria-hidden />
                {create.isPending ? 'Adding...' : 'New'}
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
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter by name..."
                className="h-9 rounded-[10px] border-0 bg-foreground/[0.055] pl-8 pr-8 text-sm shadow-none focus-visible:ring-1"
                aria-label="Filter rows"
              />
              {filter && (
                <button
                  type="button"
                  aria-label="Clear filter"
                  onClick={() => setFilter('')}
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
          {hiddenPropIds.size > 0 && (
            <p className="text-xs text-foreground/45">
              {hiddenPropIds.size} hidden{' '}
              {hiddenPropIds.size === 1 ? 'property' : 'properties'}
            </p>
          )}
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <div
          ref={virtual.scrollRef}
          onScroll={virtual.onScroll}
          className="min-w-0 flex-1 overflow-auto px-8 pb-10"
        >
          {viewLayout === 'table' ? (
            <div className="w-full">
              <table
                className="table-fixed border-separate border-spacing-0"
                style={{ width: tablePixelWidth, minWidth: '100%' }}
              >
                <colgroup>
                  <col data-column-id="name" style={{ width: nameColumnWidth }} />
                  {visibleProps.map((p) => (
                    <col
                      key={p.id}
                      data-column-id={p.id}
                      style={{
                        width: propertyColumnWidths[p.id] ?? TABLE_PROPERTY_COLUMN_WIDTH,
                      }}
                    />
                  ))}
                  <col data-column-id="add-column" />
                </colgroup>
                <thead className="sticky top-0 z-10 bg-background/95 backdrop-blur">
                  <tr>
                    <ColumnHeader
                      label="Name"
                      sortKey="any.name"
                      activeSortKey={sortKey}
                      activeSortDir={sortDir}
                      onSort={(k, d) => {
                        setSortKey(k);
                        setSortDir(d);
                      }}
                      width={nameColumnWidth}
                      onResize={(width) => resizeColumn('name', width, TABLE_NAME_COLUMN_WIDTH)}
                    />
                    {visibleProps.map((p) => (
                      <ColumnHeader
                        key={p.id}
                        propId={p.id}
                        label={p.name ?? `Untitled (${p.id.slice(0, 6)}...)`}
                        sortKey={`${typeId}.${p.id}`}
                        activeSortKey={sortKey}
                        activeSortDir={sortDir}
                        dragging={draggedColumnPropId === p.id}
                        dropTarget={columnDropTargetPropId === p.id}
                        onSort={(k, d) => {
                          setSortKey(k);
                          setSortDir(d);
                        }}
                        onColumnDragStart={(propId) => setDraggedColumnPropId(propId)}
                        onColumnDragEnd={endColumnDrag}
                        onColumnDragEnter={(propId) => {
                          if (draggedColumnPropId && draggedColumnPropId !== propId) {
                            setColumnDropTargetPropId(propId);
                          }
                        }}
                        onColumnDrop={(sourceId, targetId) => {
                          moveProperty(sourceId, targetId);
                          endColumnDrag();
                        }}
                        align={p.kind === 'number' ? 'right' : 'left'}
                        width={propertyColumnWidths[p.id] ?? TABLE_PROPERTY_COLUMN_WIDTH}
                        onResize={(width) =>
                          resizeColumn(p.id, width, TABLE_PROPERTY_COLUMN_WIDTH)
                        }
                      />
                    ))}
                    <th className="border-y border-foreground/[0.075] p-0 text-left">
                      <AddColumnPopover spaceId={spaceId} typeId={typeId} />
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {objectsQuery.isError && (
                    <tr>
                      <td
                        colSpan={visibleProps.length + 2}
                        className="px-4 py-4 text-sm text-destructive"
                      >
                        {(objectsQuery.error instanceof ApiError
                          ? objectsQuery.error.code
                          : 'unknown') +
                          ' — ' +
                          (objectsQuery.error instanceof Error
                            ? objectsQuery.error.message
                            : 'Failed to load')}
                      </td>
                    </tr>
                  )}
                  {objectsQuery.isSuccess && visibleRows.length === 0 && (
                    <tr>
                      <td
                        colSpan={visibleProps.length + 2}
                        className="border-b border-foreground/[0.045] px-4 py-10 text-left"
                      >
                        <div className="max-w-sm rounded-lg bg-foreground/[0.025] px-4 py-3">
                          <p className="text-sm font-medium text-foreground/70">
                            {filter ? 'No matches' : `No ${typeName} yet`}
                          </p>
                          <p className="mt-1 text-xs text-foreground/45">
                            {filter
                              ? 'Try a different filter or clear it from the toolbar.'
                              : 'Create the first row and it will appear in this table.'}
                          </p>
                        </div>
                      </td>
                    </tr>
                  )}
                  {objectsQuery.isSuccess && virtual.paddingTop > 0 && (
                    <SpacerRow
                      height={virtual.paddingTop}
                      colSpan={visibleProps.length + 2}
                    />
                  )}
                  {objectsQuery.isSuccess &&
                    virtualRows.map((row) => (
                      <DataRow
                        key={row.id}
                        spaceId={spaceId}
                        typeId={typeId}
                        row={row}
                        props={visibleProps}
                      />
                    ))}
                  {objectsQuery.isSuccess && virtual.paddingBottom > 0 && (
                    <SpacerRow
                      height={virtual.paddingBottom}
                      colSpan={visibleProps.length + 2}
                    />
                  )}
                  {hasNextPage && (
                    <LoadMoreRow
                      colSpan={visibleProps.length + 2}
                      loading={isFetchingNextPage}
                      onLoad={() => void fetchNextPage()}
                    />
                  )}
                  <AddRow
                    creating={create.isPending}
                    typeName={typeName}
                    propCount={visibleProps.length}
                    onCreate={() => void createRow()}
                  />
                </tbody>
              </table>
            </div>
          ) : (
            <ListRowsView
              typeId={typeId}
              rows={virtualRows}
              props={visibleProps}
              visibleCount={visibleRows.length}
              paddingTop={virtual.paddingTop}
              paddingBottom={virtual.paddingBottom}
              isSuccess={objectsQuery.isSuccess}
              error={objectsQuery.isError ? objectsQuery.error : null}
              filter={filter}
              typeName={typeName}
              creating={create.isPending}
              hasNextPage={hasNextPage}
              isFetchingNextPage={isFetchingNextPage}
              onLoadMore={() => void fetchNextPage()}
              onCreate={() => void createRow()}
            />
          )}
        </div>
        {settingsOpen && (
          <ViewSettingsPanel
            spaceId={spaceId}
            typeId={typeId}
            props={userProps}
            hiddenPropIds={hiddenPropIds}
            viewLayout={viewLayout}
            filter={filter}
            sortKey={sortKey}
            sortDir={sortDir}
            onClose={() => setSettingsOpen(false)}
            onViewLayoutChange={setViewLayout}
            onFilterChange={(next) => {
              setFilter(next);
              setFilterOpen(next.trim() !== '');
            }}
            onToggleProperty={toggleProperty}
            onMoveProperty={moveProperty}
            onSort={(key, dir) => {
              setSortKey(key);
              setSortDir(dir);
            }}
          />
        )}
      </div>
    </div>
  );
}

const ToolbarButton = forwardRef<
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

function ListTitleEditor({
  name,
  icon,
  onRename,
  onIconChange,
}: {
  name: string;
  icon?: string | undefined;
  onRename: (name: string) => void;
  onIconChange: (icon: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(name);
  const [iconOpen, setIconOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!editing) setDraft(name);
  }, [editing, name]);

  useEffect(() => {
    if (!editing) return;
    const id = window.setTimeout(() => {
      inputRef.current?.focus();
      inputRef.current?.select();
    }, 0);
    return () => window.clearTimeout(id);
  }, [editing]);

  const commit = () => {
    const next = draft.trim();
    if (next && next !== name) onRename(next);
    setEditing(false);
  };

  const cancel = () => {
    setDraft(name);
    setEditing(false);
  };

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    commit();
  };

  return (
    <div className="flex min-w-0 items-center gap-2">
      <button
        type="button"
        aria-label={icon ? `Change ${name} list icon` : `Add icon to ${name} list`}
        onClick={() => setIconOpen(true)}
        className={cn(
          'inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px]',
          'text-foreground/55 hover:bg-foreground/[0.055] hover:text-foreground',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <ListIcon icon={icon} className="h-5 w-5 text-[20px]" />
      </button>
      {editing ? (
        <form onSubmit={submit} className="min-w-0 flex-1">
          <h1 className="min-w-0">
            <Input
              ref={inputRef}
              aria-label="List name"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={commit}
              onKeyDown={(e) => {
                if (e.key === 'Escape') {
                  e.preventDefault();
                  cancel();
                }
              }}
              className="h-11 max-w-xl border-0 bg-foreground/[0.055] px-2 text-[34px] font-bold leading-none shadow-none focus-visible:ring-1"
            />
          </h1>
        </form>
      ) : (
        <h1 className="min-w-0 truncate text-[34px] font-bold leading-[1.08] text-foreground">
          <button
            type="button"
            title={`Rename ${name} list`}
            onClick={() => setEditing(true)}
            className={cn(
              'min-w-0 max-w-full truncate rounded-[10px] px-1 text-left',
              'hover:bg-foreground/[0.045]',
              'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            {name}
          </button>
        </h1>
      )}
      <ListIconDialog
        open={iconOpen}
        icon={icon}
        onOpenChange={setIconOpen}
        onSelect={onIconChange}
      />
    </div>
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
      <SortMenuContent props={props} typeId={typeId} sortKey={sortKey} onSort={onSort} />
    </DropdownMenu>
  );
}

function SortMenuContent({
  props,
  typeId,
  sortKey,
  onSort,
}: {
  props: PropertyDef[];
  typeId: string;
  sortKey: string;
  onSort: (key: string, dir: SortDir) => void;
}) {
  return (
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
  );
}

function sortLabel(props: PropertyDef[], typeId: string, sortKey: string) {
  if (sortKey === 'nav.pos') return 'Default';
  if (sortKey === 'any.name') return 'Name';
  return props.find((p) => `${typeId}.${p.id}` === sortKey)?.name ?? 'Property';
}

function sortSummary(
  props: PropertyDef[],
  typeId: string,
  sortKey: string,
  sortDir: SortDir,
) {
  const label = sortLabel(props, typeId, sortKey);
  if (sortKey === 'nav.pos' && sortDir === 'asc') return 'Default';
  return `${label}, ${sortDir === 'desc' ? 'desc' : 'asc'}`;
}

function ViewSettingsPanel({
  spaceId,
  typeId,
  props,
  hiddenPropIds,
  viewLayout,
  filter,
  sortKey,
  sortDir,
  onClose,
  onViewLayoutChange,
  onFilterChange,
  onToggleProperty,
  onMoveProperty,
  onSort,
}: {
  spaceId: string;
  typeId: string;
  props: PropertyDef[];
  hiddenPropIds: Set<string>;
  viewLayout: TableViewLayout;
  filter: string;
  sortKey: string;
  sortDir: SortDir;
  onClose: () => void;
  onViewLayoutChange: (layout: TableViewLayout) => void;
  onFilterChange: (next: string) => void;
  onToggleProperty: (propId: string, visible: boolean) => void;
  onMoveProperty: (sourceId: string, targetId: string) => void;
  onSort: (key: string, dir: SortDir) => void;
}) {
  const [screen, setScreen] = useState<SettingsScreen>('main');
  const [selectedPropId, setSelectedPropId] = useState<string | null>(null);
  const [newPropertyName, setNewPropertyName] = useState('');
  const addProperty = useAddPropertyToType(spaceId);
  const hiddenCount = hiddenPropIds.size;
  const shownProps = props.filter((p) => !hiddenPropIds.has(p.id));
  const hiddenProps = props.filter((p) => hiddenPropIds.has(p.id));
  const selectedProp = props.find((p) => p.id === selectedPropId) ?? null;
  const screenTitle: Record<SettingsScreen, string> = {
    main: 'View settings',
    layout: 'Layout',
    'property-visibility': 'Property visibility',
    filter: 'Filter',
    sort: 'Sort',
    'default-template': 'Default template',
    'connected-templates': 'Connected templates',
    properties: 'Properties',
    'add-property': 'Add property',
    'edit-property': 'Edit property',
  };

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
      setScreen('properties');
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to add property';
      toast.error(`${code}: ${msg}`);
    }
  };

  return (
    <aside
      aria-label="View settings"
      className="w-[22rem] shrink-0 overflow-auto border-l border-foreground/[0.08] bg-background px-5 py-4"
    >
      <PanelHeader
        title={screenTitle[screen]}
        subtitle={screen === 'main' ? 'Local table controls' : undefined}
        onBack={screen === 'main' ? undefined : () => setScreen('main')}
        onClose={onClose}
      />

      {screen === 'main' && (
        <>
          <Input
            aria-label="View name"
            value="All"
            readOnly
            className="mb-4 h-9 bg-foreground/[0.04]"
          />

          <div className="space-y-1">
            <SettingRowButton
              label="Layout"
              value={viewLayout === 'table' ? 'Table' : 'List'}
              onClick={() => setScreen('layout')}
            />
            <SettingRowButton
              label="Property visibility"
              value={hiddenCount === 0 ? `${props.length}` : `${hiddenCount} hidden`}
              onClick={() => setScreen('property-visibility')}
            />
            <SettingRowButton
              label="Filter"
              value={filter.trim() === '' ? 'None' : filter.trim()}
              onClick={() => setScreen('filter')}
            />
            <SettingRowButton
              label="Sort"
              value={sortSummary(props, typeId, sortKey, sortDir)}
              onClick={() => setScreen('sort')}
            />
          </div>

          <div className="my-4 h-px bg-foreground/[0.08]" />

          <p className="mb-2 px-2 text-xs font-medium text-foreground/45">
            Collection settings
          </p>
          <div className="space-y-1">
            <SettingRowButton
              label="Default template"
              value="Page"
              onClick={() => setScreen('default-template')}
            />
            <SettingRowButton
              label="Connected templates"
              value="0"
              onClick={() => setScreen('connected-templates')}
            />
            <SettingRowButton
              label="Properties"
              value={`${props.length}`}
              onClick={() => setScreen('properties')}
            />
          </div>
        </>
      )}

      {screen === 'property-visibility' && (
        <div className="space-y-5">
          <section>
            <div className="mb-2 flex items-center justify-between gap-3">
              <p className="text-xs font-medium text-foreground/45">Shown</p>
              {shownProps.length > 0 && (
                <button
                  type="button"
                  onClick={() => props.forEach((p) => onToggleProperty(p.id, false))}
                  className="text-xs font-medium text-accent hover:opacity-80"
                >
                  Hide all
                </button>
              )}
            </div>
            <div className="space-y-1">
              <PropertyVisibilityItem name="Name" fixed shown />
              <PropertyVisibilityList
                props={shownProps}
                shown
                onMove={onMoveProperty}
                onToggle={(propId) => onToggleProperty(propId, false)}
              />
            </div>
          </section>

          <section>
            <div className="mb-2 flex items-center justify-between gap-3">
              <p className="text-xs font-medium text-foreground/45">Hidden</p>
              {hiddenProps.length > 0 && (
                <button
                  type="button"
                  onClick={() => props.forEach((p) => onToggleProperty(p.id, true))}
                  className="text-xs font-medium text-accent hover:opacity-80"
                >
                  Show all
                </button>
              )}
            </div>
            <div className="space-y-1">
              {hiddenProps.length === 0 ? (
                <p className="rounded-md px-2 py-1.5 text-sm text-foreground/45">
                  No hidden properties
                </p>
              ) : (
                <PropertyVisibilityList
                  props={hiddenProps}
                  shown={false}
                  onMove={onMoveProperty}
                  onToggle={(propId) => onToggleProperty(propId, true)}
                />
              )}
            </div>
          </section>
        </div>
      )}

      {screen === 'filter' && (
        <div className="space-y-3">
          <p className="text-sm text-foreground/55">Filter loaded rows by name.</p>
          <div className="relative">
            <Search
              className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground/40"
              aria-hidden
            />
            <Input
              value={filter}
              onChange={(e) => onFilterChange(e.target.value)}
              placeholder="Filter by name..."
              className="h-9 pl-7 text-sm"
              aria-label="Settings filter rows"
            />
          </div>
          {filter.trim() !== '' && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onFilterChange('')}
            >
              Clear filter
            </Button>
          )}
        </div>
      )}

      {screen === 'sort' && (
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
                  label={p.name ?? `Untitled (${p.id.slice(0, 6)}...)`}
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
      )}

      {screen === 'layout' && (
        <div className="space-y-2">
          <LayoutOption
            icon={Table2}
            label="Table"
            description="Spreadsheet-style rows and editable cells."
            active={viewLayout === 'table'}
            onClick={() => onViewLayoutChange('table')}
          />
          <LayoutOption
            icon={ListLayoutIcon}
            label="List"
            description="Readable rows with property previews."
            active={viewLayout === 'list'}
            onClick={() => onViewLayoutChange('list')}
          />
        </div>
      )}

      {screen === 'properties' && (
        <div className="space-y-2">
          <PropertyListRow name="Name" kindLabel="Title" fixed />
          {props.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-foreground/45">No properties yet</p>
          ) : (
            props.map((p) => (
              <PropertyListRow
                key={p.id}
                name={p.name ?? `Untitled (${p.id.slice(0, 6)}...)`}
                kindLabel={propertyKindLabel(uiKind(p))}
                onClick={() => {
                  setSelectedPropId(p.id);
                  setScreen('edit-property');
                }}
              />
            ))
          )}
          <button
            type="button"
            onClick={() => setScreen('add-property')}
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
      )}

      {screen === 'add-property' && (
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
      )}

      {screen === 'edit-property' &&
        (selectedProp ? (
          <EditPropertyScreen prop={selectedProp} />
        ) : (
          <PlaceholderScreen
            title="Property not found"
            body="The property list changed. Go back and select it again."
          />
        ))}

      {screen === 'default-template' && (
        <PlaceholderScreen
          title="Page"
          body="Template selection needs a template API. This row is here so the settings structure is ready."
        />
      )}

      {screen === 'connected-templates' && (
        <PlaceholderScreen
          title="No connected templates"
          body="Connected templates are not part of the current HTTP surface yet."
        />
      )}
    </aside>
  );
}

function PanelHeader({
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

function propertyKindLabel(kind: UIPropertyKind) {
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

function PropertyListRow({
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
      <GripVertical className="h-3.5 w-3.5 shrink-0 text-foreground/30" aria-hidden />
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

function CreatePropertyOption({
  label,
  icon: Icon,
  disabled,
  onClick,
}: {
  label: string;
  icon: typeof Text;
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

function EditPropertyScreen({ prop }: { prop: PropertyDef }) {
  const kind = uiKind(prop);
  const name = prop.name ?? `Untitled (${prop.id.slice(0, 6)}...)`;

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

function SettingRowIcon({ icon: Icon, label }: { icon: typeof Text; label: string }) {
  return (
    <div className="flex h-8 items-center gap-2 rounded-md px-2 text-sm text-foreground/55">
      <Icon className="h-3.5 w-3.5 shrink-0 text-foreground/40" aria-hidden />
      <span className="min-w-0 truncate">{label}</span>
    </div>
  );
}

interface PropertyVisibilityDragState {
  propId: string;
  pointerId: number;
  startY: number;
  currentY: number;
  targetId: string;
}

function PropertyVisibilityList({
  props,
  shown,
  onMove,
  onToggle,
}: {
  props: PropertyDef[];
  shown: boolean;
  onMove: (sourceId: string, targetId: string) => void;
  onToggle: (propId: string) => void;
}) {
  const rowRefs = useRef(new Map<string, HTMLDivElement>());
  const [drag, setDrag] = useState<PropertyVisibilityDragState | null>(null);
  const dragRef = useRef<PropertyVisibilityDragState | null>(null);
  const propIds = useMemo(() => props.map((p) => p.id), [props]);
  const dragging = drag != null;

  const setDragState = useCallback((next: PropertyVisibilityDragState | null) => {
    dragRef.current = next;
    setDrag(next);
  }, []);

  useEffect(() => {
    if (!dragging) return;
    const originalCursor = document.body.style.cursor;
    const originalUserSelect = document.body.style.userSelect;
    document.body.style.cursor = 'grabbing';
    document.body.style.userSelect = 'none';
    return () => {
      document.body.style.cursor = originalCursor;
      document.body.style.userSelect = originalUserSelect;
    };
  }, [dragging]);

  const registerRow = useCallback((propId: string, node: HTMLDivElement | null) => {
    if (node) rowRefs.current.set(propId, node);
    else rowRefs.current.delete(propId);
  }, []);

  const targetForPointer = useCallback(
    (clientY: number) => {
      if (propIds.length === 0) return null;
      const rows = propIds
        .map((id) => {
          const node = rowRefs.current.get(id);
          if (!node) return null;
          const rect = node.getBoundingClientRect();
          return { id, midpoint: rect.top + rect.height / 2 };
        })
        .filter((row): row is { id: string; midpoint: number } => row != null);
      if (rows.length === 0) return null;

      let targetId = rows[0]?.id ?? null;
      for (const row of rows) {
        if (clientY >= row.midpoint) targetId = row.id;
        else break;
      }
      return targetId;
    },
    [propIds],
  );

  const beginDrag = useCallback(
    (propId: string, e: ReactPointerEvent<HTMLButtonElement>) => {
      if (e.button != null && e.button !== 0) return;
      e.preventDefault();
      const pointerId = e.pointerId || 1;
      e.currentTarget.setPointerCapture?.(pointerId);
      const targetId = targetForPointer(e.clientY) ?? propId;
      setDragState({
        propId,
        pointerId,
        startY: e.clientY,
        currentY: e.clientY,
        targetId,
      });
    },
    [setDragState, targetForPointer],
  );

  const updateDrag = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      const current = dragRef.current;
      if (!current || current.pointerId !== (e.pointerId || 1)) return;
      const targetId = targetForPointer(e.clientY) ?? current.propId;
      setDragState({
        ...current,
        currentY: e.clientY,
        targetId,
      });
    },
    [setDragState, targetForPointer],
  );

  const finishDrag = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      const current = dragRef.current;
      if (!current || current.pointerId !== (e.pointerId || 1)) return;
      e.currentTarget.releasePointerCapture?.(current.pointerId);
      setDragState(null);
      if (current.targetId !== current.propId) {
        onMove(current.propId, current.targetId);
      }
    },
    [onMove, setDragState],
  );

  const cancelDrag = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      const current = dragRef.current;
      if (!current || current.pointerId !== (e.pointerId || 1)) return;
      e.currentTarget.releasePointerCapture?.(current.pointerId);
      setDragState(null);
    },
    [setDragState],
  );

  const rowStyle = useCallback(
    (propId: string): CSSProperties | undefined => {
      const current = drag;
      if (!current) return undefined;
      const sourceIndex = propIds.indexOf(current.propId);
      const targetIndex = propIds.indexOf(current.targetId);
      const index = propIds.indexOf(propId);
      if (sourceIndex < 0 || targetIndex < 0 || index < 0) return undefined;

      let translateY = 0;
      if (propId === current.propId) {
        translateY = current.currentY - current.startY;
      } else if (sourceIndex < targetIndex && index > sourceIndex && index <= targetIndex) {
        translateY = -PROPERTY_VISIBILITY_ROW_STEP;
      } else if (sourceIndex > targetIndex && index >= targetIndex && index < sourceIndex) {
        translateY = PROPERTY_VISIBILITY_ROW_STEP;
      }

      return { transform: `translate3d(0, ${translateY}px, 0)` };
    },
    [drag, propIds],
  );

  return (
    <>
      {props.map((p) => (
        <PropertyVisibilityItem
          key={p.id}
          propId={p.id}
          name={p.name ?? `Untitled (${p.id.slice(0, 6)}...)`}
          shown={shown}
          dragging={drag?.propId === p.id}
          dragActive={dragging}
          style={rowStyle(p.id)}
          setRowRef={(node) => registerRow(p.id, node)}
          onPointerDragStart={beginDrag}
          onPointerDragMove={updateDrag}
          onPointerDragEnd={finishDrag}
          onPointerDragCancel={cancelDrag}
          onToggle={() => onToggle(p.id)}
        />
      ))}
    </>
  );
}

function PropertyVisibilityItem({
  propId,
  name,
  shown,
  fixed,
  dragging,
  dragActive,
  style,
  setRowRef,
  onPointerDragStart,
  onPointerDragMove,
  onPointerDragEnd,
  onPointerDragCancel,
  onToggle,
}: {
  propId?: string;
  name: string;
  shown: boolean;
  fixed?: boolean;
  dragging?: boolean;
  dragActive?: boolean;
  style?: CSSProperties | undefined;
  setRowRef?: (node: HTMLDivElement | null) => void;
  onPointerDragStart?: (
    propId: string,
    event: ReactPointerEvent<HTMLButtonElement>,
  ) => void;
  onPointerDragMove?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onPointerDragEnd?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onPointerDragCancel?: (event: ReactPointerEvent<HTMLButtonElement>) => void;
  onToggle?: () => void;
}) {
  const draggable = !fixed && propId != null;
  return (
    <div
      ref={setRowRef}
      style={style}
      data-property-visibility-row={propId}
      className={cn(
        'relative flex h-8 items-center gap-2 rounded-md px-2 text-sm text-foreground/80 will-change-transform',
        !dragging &&
          'transition-[transform,background-color,opacity] duration-150 ease-out',
        dragging &&
          'z-20 bg-background shadow-lg shadow-black/15 ring-1 ring-accent/35 transition-[background-color,box-shadow] duration-75',
        dragActive && !dragging && 'pointer-events-none',
        draggable && 'hover:bg-foreground/[0.04]',
      )}
      data-dragging={dragging ? 'true' : 'false'}
    >
      <button
        type="button"
        aria-label={draggable ? `Drag ${name}` : undefined}
        onPointerDown={(e) => {
          if (!draggable || !propId) return;
          onPointerDragStart?.(propId, e);
        }}
        onPointerMove={onPointerDragMove}
        onPointerUp={onPointerDragEnd}
        onPointerCancel={onPointerDragCancel}
        className={cn(
          'inline-flex h-7 w-5 shrink-0 cursor-grab touch-none items-center justify-center rounded text-foreground/30',
          draggable &&
            'hover:bg-foreground/5 hover:text-foreground/65 active:cursor-grabbing',
          !draggable && 'cursor-default',
          dragging && 'cursor-grabbing bg-accent/10 text-accent',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        )}
      >
        <GripVertical className="h-3.5 w-3.5" aria-hidden />
      </button>
      <span className="min-w-0 flex-1 truncate">{name}</span>
      {fixed ? (
        <Eye className="h-3.5 w-3.5 shrink-0 text-foreground/30" aria-hidden />
      ) : (
        <button
          type="button"
          aria-label={shown ? `Hide ${name}` : `Show ${name}`}
          onClick={onToggle}
          className={cn(
            'inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-foreground/45',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          {shown ? (
            <EyeOff className="h-3.5 w-3.5" aria-hidden />
          ) : (
            <Eye className="h-3.5 w-3.5" aria-hidden />
          )}
        </button>
      )}
    </div>
  );
}

function SortOption({
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

function LayoutOption({
  icon: Icon,
  label,
  description,
  active,
  onClick,
}: {
  icon: typeof Text;
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

function PlaceholderScreen({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded-md border border-foreground/[0.08] px-3 py-3">
      <p className="text-sm font-medium text-foreground">{title}</p>
      <p className="mt-1 text-sm text-foreground/50">{body}</p>
    </div>
  );
}

function SettingRow({ label, value }: { label: string; value?: string }) {
  return (
    <div className="flex h-9 items-center justify-between gap-3 rounded-md px-2 text-sm text-foreground/45">
      <span className="min-w-0 truncate">{label}</span>
      {value && <span className="shrink-0 text-foreground/40">{value}</span>}
    </div>
  );
}

const SettingRowButton = forwardRef<
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

function ColumnHeader({
  propId,
  label,
  sortKey,
  activeSortKey,
  activeSortDir,
  onSort,
  dragging,
  dropTarget,
  onColumnDragStart,
  onColumnDragEnd,
  onColumnDragEnter,
  onColumnDrop,
  align = 'left',
  width,
  onResize,
}: {
  propId?: string;
  label: string;
  sortKey: string;
  activeSortKey: string;
  activeSortDir: SortDir;
  onSort: (key: string, dir: SortDir) => void;
  dragging?: boolean;
  dropTarget?: boolean;
  onColumnDragStart?: (propId: string) => void;
  onColumnDragEnd?: () => void;
  onColumnDragEnter?: (propId: string) => void;
  onColumnDrop?: (sourceId: string, targetId: string) => void;
  align?: 'left' | 'right';
  width?: number;
  onResize?: (width: number) => void;
}) {
  const active = activeSortKey === sortKey && activeSortDir != null;
  const draggable = propId != null;
  const resizable = width != null && onResize != null;
  const [resizing, setResizing] = useState(false);
  const onClick = () => {
    if (activeSortKey !== sortKey) {
      onSort(sortKey, 'asc');
    } else if (activeSortDir === 'asc') {
      onSort(sortKey, 'desc');
    } else if (activeSortDir === 'desc') {
      onSort('nav.pos', 'asc');
    } else {
      onSort(sortKey, 'asc');
    }
  };
  const startResize = (e: ReactMouseEvent<HTMLButtonElement>) => {
    if (width == null || onResize == null || e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();

    const startX = e.clientX;
    const startWidth = width;
    const originalCursor = document.body.style.cursor;
    const originalUserSelect = document.body.style.userSelect;
    setResizing(true);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';

    const onMove = (event: MouseEvent) => {
      onResize(startWidth + event.clientX - startX);
    };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      document.body.style.cursor = originalCursor;
      document.body.style.userSelect = originalUserSelect;
      setResizing(false);
    };

    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp, { once: true });
  };

  return (
    <th
      onDragEnter={(e) => {
        if (!draggable || !propId) return;
        e.preventDefault();
        onColumnDragEnter?.(propId);
      }}
      onDragOver={(e) => {
        if (!draggable) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
      }}
      onDrop={(e) => {
        if (!draggable || !propId) return;
        e.preventDefault();
        const sourceId =
          e.dataTransfer.getData('application/x-any-property-id') ||
          e.dataTransfer.getData('text/plain');
        if (sourceId) onColumnDrop?.(sourceId, propId);
      }}
      className={cn(
        'group relative border-y border-foreground/[0.075] bg-background p-0 transition-colors',
        dropTarget && 'bg-accent/[0.06] ring-2 ring-inset ring-accent',
        dragging && 'opacity-45',
        resizing && 'z-20',
      )}
    >
      <div className="flex h-10 items-center">
        {draggable && (
          <button
            type="button"
            draggable
            aria-label={`Drag ${label} column`}
            onClick={(e) => e.stopPropagation()}
            onDragStart={(e) => {
              if (!propId) return;
              e.dataTransfer.effectAllowed = 'move';
              e.dataTransfer.setData('application/x-any-property-id', propId);
              e.dataTransfer.setData('text/plain', propId);
              onColumnDragStart?.(propId);
            }}
            onDragEnd={onColumnDragEnd}
            className={cn(
              'ml-1 inline-flex h-7 w-6 shrink-0 cursor-grab items-center justify-center rounded text-foreground/25',
              'opacity-0 transition-opacity hover:bg-foreground/5 hover:text-foreground/55 active:cursor-grabbing',
              'group-hover:opacity-100 group-focus-within:opacity-100',
              'focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            )}
          >
            <GripVertical className="h-3.5 w-3.5" aria-hidden />
          </button>
        )}
        <button
          type="button"
          onClick={onClick}
          className={cn(
            'flex h-full min-w-0 flex-1 items-center gap-1 px-3 text-[12px] font-semibold uppercase tracking-[0.06em]',
            draggable && 'pl-1',
            align === 'right' ? 'justify-end' : 'justify-start',
            'text-foreground/48 hover:text-foreground/78',
            'focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-inset',
          )}
        >
          <span className="truncate">{label}</span>
          {active && activeSortDir === 'asc' && (
            <ChevronUp className="h-3 w-3 shrink-0" aria-hidden />
          )}
          {active && activeSortDir === 'desc' && (
            <ChevronDown className="h-3 w-3 shrink-0" aria-hidden />
          )}
        </button>
      </div>
      {resizable && (
        <button
          type="button"
          aria-label={`Resize ${label} column`}
          title={`Resize ${label} column`}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
          }}
          onMouseDown={startResize}
          className={cn(
            'absolute right-0 top-0 z-10 h-full w-2 translate-x-1/2 cursor-col-resize',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
            'after:absolute after:right-[3px] after:top-1 after:h-[calc(100%-0.5rem)] after:w-px after:rounded-full',
            'after:bg-foreground/0 after:transition-colors hover:after:bg-accent/80 focus-visible:after:bg-accent/80',
            'group-hover:after:bg-foreground/20',
            resizing && 'after:bg-accent',
          )}
        />
      )}
    </th>
  );
}

// ---------------- Helpers -------------------------------------------

function isUserKind(k: PropertyKind): boolean {
  return (
    k === 'string' ||
    k === 'number' ||
    k === 'boolean' ||
    k === 'null' ||
    k === 'array' ||
    k === 'object'
  );
}
