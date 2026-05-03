import { useCallback, useEffect, useRef, useState } from 'react';
import {
  TABLE_ADD_COLUMN_WIDTH,
  TABLE_NAME_COLUMN_WIDTH,
  TABLE_PROPERTY_COLUMN_WIDTH,
} from '@/atoms';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';
import { AddColumnPopover } from './AddColumnPopover';
import { GalleryRowsView } from './GalleryRowsView';
import { ListRowsView, LIST_ROW_HEIGHT } from './ListRowsView';
import { ListTitleEditor } from './ListTitleEditor';
import { TableColumnHeader } from './TableColumnHeader';
import { TableSettingsPanel } from './TableSettingsPanel';
import { TableToolbar } from './TableToolbar';
import {
  AddRow,
  DataRow,
  LoadMoreRow,
  SpacerRow,
  TABLE_ROW_HEIGHT,
} from './TableRows';
import {
  autoFitNameColumnWidth,
  autoFitPropertyColumnWidth,
} from './tableColumnSizing';
import { useDataViewLayout } from './useDataViewLayout';
import { useTableProperties } from './useTableProperties';
import { useTableViewController } from './useTableViewController';
import { useVirtualRows } from '@/shared';

interface TableViewProps {
  typeId: string;
}

/**
 * Database-style view of a type.
 *
 * Keep this component as composition only. Query/paging/filter/sort live in
 * useTableViewController, property visibility/order/widths live in
 * useTableProperties, and new layouts should render from those same surfaces.
 */
export function TableView({ typeId }: TableViewProps) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsMounted = useDeferredPresence(settingsOpen, 220);
  const tableHostRef = useRef<HTMLDivElement | null>(null);
  const [tableHostWidth, setTableHostWidth] = useState(0);
  const { viewLayout, setViewLayout } = useDataViewLayout(typeId);
  const rowHeight = viewLayout === 'list' ? LIST_ROW_HEIGHT : TABLE_ROW_HEIGHT;
  const table = useTableViewController(typeId);
  const virtual = useVirtualRows({
    count: table.visibleRows.length,
    rowHeight,
    overscan: 10,
  });
  const virtualRows = table.visibleRows.slice(virtual.startIndex, virtual.endIndex);
  const props = useTableProperties(table.spaceId, typeId);
  const {
    createRow: createRowFromController,
    debouncedFilter,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
    visibleRows,
  } = table;

  useEffect(() => {
    if (viewLayout === 'gallery') return;
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
    visibleRows.length,
    viewLayout,
    virtual.endIndex,
  ]);

  useEffect(() => {
    const measure = () => {
      setTableHostWidth(tableHostRef.current?.clientWidth ?? 0);
    };

    measure();
    const node = tableHostRef.current;
    if (!node || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, [viewLayout]);

  const createRow = useCallback(
    () => void createRowFromController(),
    [createRowFromController],
  );
  const loadMore = useCallback(() => void fetchNextPage(), [fetchNextPage]);
  const autoResizeAllColumns = useCallback(() => {
    props.resizeColumn(
      'name',
      autoFitNameColumnWidth(table.visibleRows),
      TABLE_NAME_COLUMN_WIDTH,
    );
    for (const prop of props.visibleProps) {
      props.resizeColumn(
        prop.id,
        autoFitPropertyColumnWidth({
          rows: table.visibleRows,
          typeId,
          prop,
        }),
        TABLE_PROPERTY_COLUMN_WIDTH,
      );
    }
  }, [props, table.visibleRows, typeId]);

  const spaceId = table.spaceId;
  if (!spaceId) return null;

  const colSpan = props.visibleProps.length + 3;
  const fillerColumnWidth = Math.max(0, tableHostWidth - props.tablePixelWidth);
  const renderedTableWidth = props.tablePixelWidth + fillerColumnWidth;

  return (
    <div className="flex h-full flex-col bg-background">
      <header className="shrink-0 px-8 pb-3 pt-8">
        <div className="mx-auto flex w-full max-w-[1180px] flex-col gap-6">
          <ListTitleEditor
            name={table.typeName}
            icon={table.overrideTypeIcon}
            onRename={(name) => table.setTypeMeta({ name })}
            onIconChange={(icon) => table.setTypeMeta({ icon })}
          />

          <TableToolbar
            typeId={typeId}
            props={props.userProps}
            layout={viewLayout}
            filter={table.filter}
            filterOpen={table.filterOpen}
            hiddenPropCount={props.hiddenPropIds.size}
            sortKey={table.sortKey}
            sortDir={table.sortDir}
            settingsOpen={settingsOpen}
            createPending={table.create.isPending}
            onLayoutChange={setViewLayout}
            onFilterChange={table.setFilter}
            onFilterOpenChange={table.setFilterOpen}
            onSort={table.setSort}
            onSettingsOpenChange={setSettingsOpen}
            onCreate={createRow}
          />
        </div>
      </header>

      <div className="relative flex min-h-0 flex-1">
        {viewLayout === 'gallery' ? (
          <GalleryRowsView
            typeId={typeId}
            rows={table.visibleRows}
            props={props.visibleProps}
            isSuccess={table.objectsQuery.isSuccess}
            error={table.objectsQuery.isError ? table.objectsQuery.error : null}
            filter={table.filter}
            typeName={table.typeName}
            creating={table.create.isPending}
            hasNextPage={table.hasNextPage}
            isFetchingNextPage={table.isFetchingNextPage}
            onLoadMore={loadMore}
            onCreate={createRow}
          />
        ) : (
          <div
            ref={virtual.scrollRef}
            onScroll={virtual.onScroll}
            className="min-w-0 flex-1 overflow-auto px-8 pb-10"
          >
            {viewLayout === 'table' ? (
            <div ref={tableHostRef} className="w-full">
              <table
                className="table-fixed border-separate border-spacing-0"
                style={{ width: renderedTableWidth }}
              >
                <colgroup>
                  <col data-column-id="name" style={{ width: props.nameColumnWidth }} />
                  {props.visibleProps.map((p) => (
                    <col
                      key={p.id}
                      data-column-id={p.id}
                      style={{
                        width:
                          props.propertyColumnWidths[p.id] ?? TABLE_PROPERTY_COLUMN_WIDTH,
                      }}
                    />
                  ))}
                  <col
                    data-column-id="add-column"
                    style={{ width: TABLE_ADD_COLUMN_WIDTH }}
                  />
                  <col
                    data-column-id="table-filler"
                    style={{ width: fillerColumnWidth }}
                  />
                </colgroup>
                <thead className="sticky top-0 z-10 bg-background/95 backdrop-blur">
                  <tr>
                    <TableColumnHeader
                      label="Name"
                      sortKey="any.name"
                      activeSortKey={table.sortKey}
                      activeSortDir={table.sortDir}
                      onSort={table.setSort}
                      width={props.nameColumnWidth}
                      onResize={(width) =>
                        props.resizeColumn('name', width, TABLE_NAME_COLUMN_WIDTH)
                      }
                      onAutoResize={() =>
                        props.resizeColumn(
                          'name',
                          autoFitNameColumnWidth(table.visibleRows),
                          TABLE_NAME_COLUMN_WIDTH,
                        )
                      }
                      onAutoResizeAll={autoResizeAllColumns}
                    />
                    {props.visibleProps.map((p) => (
                      <TableColumnHeader
                        key={p.id}
                        propId={p.id}
                        label={p.name ?? `Untitled (${p.id.slice(0, 6)}...)`}
                        sortKey={`${typeId}.${p.id}`}
                        activeSortKey={table.sortKey}
                        activeSortDir={table.sortDir}
                        dragging={props.draggedColumnPropId === p.id}
                        dropTarget={props.columnDropTargetPropId === p.id}
                        onSort={table.setSort}
                        onColumnDragStart={props.setDraggedColumnPropId}
                        onColumnDragEnd={props.endColumnDrag}
                        onColumnDragEnter={(propId) => {
                          if (
                            props.draggedColumnPropId &&
                            props.draggedColumnPropId !== propId
                          ) {
                            props.setColumnDropTargetPropId(propId);
                          }
                        }}
                        onColumnDrop={(sourceId, targetId) => {
                          props.moveProperty(sourceId, targetId);
                          props.endColumnDrag();
                        }}
                        align={p.kind === 'number' ? 'right' : 'left'}
                        width={
                          props.propertyColumnWidths[p.id] ?? TABLE_PROPERTY_COLUMN_WIDTH
                        }
                        onResize={(width) =>
                          props.resizeColumn(p.id, width, TABLE_PROPERTY_COLUMN_WIDTH)
                        }
                        onAutoResize={() =>
                          props.resizeColumn(
                            p.id,
                            autoFitPropertyColumnWidth({
                              rows: table.visibleRows,
                              typeId,
                              prop: p,
                            }),
                            TABLE_PROPERTY_COLUMN_WIDTH,
                          )
                        }
                        onAutoResizeAll={autoResizeAllColumns}
                      />
                    ))}
                    <th className="border-y border-foreground/[0.075] p-0 text-left">
                      <AddColumnPopover spaceId={spaceId} typeId={typeId} />
                    </th>
                    <th
                      aria-hidden
                      className="border-y border-foreground/[0.075] bg-background p-0"
                    />
                  </tr>
                </thead>
                <tbody>
                  {table.objectsQuery.isError && (
                    <tr>
                      <td colSpan={colSpan} className="px-4 py-4 text-sm text-destructive">
                        {(table.objectsQuery.error instanceof ApiError
                          ? table.objectsQuery.error.code
                          : 'unknown') +
                          ' — ' +
                          (table.objectsQuery.error instanceof Error
                            ? table.objectsQuery.error.message
                            : 'Failed to load')}
                      </td>
                    </tr>
                  )}
                  {table.objectsQuery.isSuccess && table.visibleRows.length === 0 && (
                    <tr>
                      <td
                        colSpan={colSpan}
                        className="border-b border-foreground/[0.045] px-4 py-10 text-left"
                      >
                        <div className="max-w-sm rounded-lg bg-foreground/[0.025] px-4 py-3">
                          <p className="text-sm font-medium text-foreground/70">
                            {table.filter ? 'No matches' : `No ${table.typeName} yet`}
                          </p>
                          <p className="mt-1 text-xs text-foreground/45">
                            {table.filter
                              ? 'Try a different filter or clear it from the toolbar.'
                              : 'Create the first row and it will appear in this table.'}
                          </p>
                        </div>
                      </td>
                    </tr>
                  )}
                  {table.objectsQuery.isSuccess && virtual.paddingTop > 0 && (
                    <SpacerRow height={virtual.paddingTop} colSpan={colSpan} />
                  )}
                  {table.objectsQuery.isSuccess &&
                    virtualRows.map((row) => (
                      <DataRow
                        key={row.id}
                        spaceId={spaceId}
                        typeId={typeId}
                        row={row}
                        props={props.visibleProps}
                      />
                    ))}
                  {table.objectsQuery.isSuccess && virtual.paddingBottom > 0 && (
                    <SpacerRow height={virtual.paddingBottom} colSpan={colSpan} />
                  )}
                  {table.hasNextPage && (
                    <LoadMoreRow
                      colSpan={colSpan}
                      loading={table.isFetchingNextPage}
                      onLoad={loadMore}
                    />
                  )}
                  <AddRow
                    creating={table.create.isPending}
                    typeName={table.typeName}
                    propCount={props.visibleProps.length}
                    onCreate={createRow}
                  />
                </tbody>
              </table>
            </div>
          ) : (
            <ListRowsView
              typeId={typeId}
              rows={virtualRows}
              props={props.visibleProps}
              visibleCount={table.visibleRows.length}
              paddingTop={virtual.paddingTop}
              paddingBottom={virtual.paddingBottom}
              isSuccess={table.objectsQuery.isSuccess}
              error={table.objectsQuery.isError ? table.objectsQuery.error : null}
              filter={table.filter}
              typeName={table.typeName}
              creating={table.create.isPending}
              hasNextPage={table.hasNextPage}
              isFetchingNextPage={table.isFetchingNextPage}
              onLoadMore={loadMore}
              onCreate={createRow}
            />
          )}
          </div>
        )}

        <div
          aria-hidden={!settingsOpen}
          className={cn(
            'absolute inset-y-0 right-0 z-20 overflow-hidden transition-[width,opacity] duration-200 ease-out motion-reduce:transition-none',
            settingsOpen
              ? 'pointer-events-auto w-[22rem] opacity-100'
              : 'pointer-events-none w-0 opacity-0',
          )}
        >
          <div className="h-full w-[22rem]">
            {settingsMounted && (
              <TableSettingsPanel
                spaceId={spaceId}
                typeId={typeId}
                props={props.userProps}
                hiddenPropIds={props.hiddenPropIds}
                viewLayout={viewLayout}
                filter={table.filter}
                sortKey={table.sortKey}
                sortDir={table.sortDir}
                onClose={() => setSettingsOpen(false)}
                onViewLayoutChange={setViewLayout}
                onFilterChange={(next) => {
                  table.setFilter(next);
                  table.setFilterOpen(next.trim() !== '');
                }}
                onToggleProperty={props.toggleProperty}
                onMoveProperty={props.moveProperty}
                onSort={table.setSort}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function useDeferredPresence(present: boolean, delayMs: number) {
  const [rendered, setRendered] = useState(present);

  useEffect(() => {
    if (present) {
      setRendered(true);
      return;
    }

    const timeout = window.setTimeout(() => setRendered(false), delayMs);
    return () => window.clearTimeout(timeout);
  }, [delayMs, present]);

  return present || rendered;
}
