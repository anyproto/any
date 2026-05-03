import { useState } from 'react';
import {
  TABLE_NAME_COLUMN_WIDTH,
  TABLE_PROPERTY_COLUMN_WIDTH,
} from '@/atoms';
import { ApiError } from '@/lib/api/client';
import { AddColumnPopover } from './AddColumnPopover';
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
import { useDataViewLayout } from './useDataViewLayout';
import { useTableProperties } from './useTableProperties';
import { useTableViewController } from './useTableViewController';

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
  const { viewLayout, setViewLayout } = useDataViewLayout(typeId);
  const rowHeight = viewLayout === 'list' ? LIST_ROW_HEIGHT : TABLE_ROW_HEIGHT;
  const table = useTableViewController(typeId, rowHeight);
  const props = useTableProperties(table.spaceId, typeId);

  const spaceId = table.spaceId;
  if (!spaceId) return null;

  const createRow = () => void table.createRow();
  const colSpan = props.visibleProps.length + 2;

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

      <div className="flex min-h-0 flex-1">
        <div
          ref={table.virtual.scrollRef}
          onScroll={table.virtual.onScroll}
          className="min-w-0 flex-1 overflow-auto px-8 pb-10"
        >
          {viewLayout === 'table' ? (
            <div className="w-full">
              <table
                className="table-fixed border-separate border-spacing-0"
                style={{ width: props.tablePixelWidth, minWidth: '100%' }}
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
                  <col data-column-id="add-column" />
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
                      />
                    ))}
                    <th className="border-y border-foreground/[0.075] p-0 text-left">
                      <AddColumnPopover spaceId={spaceId} typeId={typeId} />
                    </th>
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
                  {table.objectsQuery.isSuccess && table.virtual.paddingTop > 0 && (
                    <SpacerRow height={table.virtual.paddingTop} colSpan={colSpan} />
                  )}
                  {table.objectsQuery.isSuccess &&
                    table.virtualRows.map((row) => (
                      <DataRow
                        key={row.id}
                        spaceId={spaceId}
                        typeId={typeId}
                        row={row}
                        props={props.visibleProps}
                      />
                    ))}
                  {table.objectsQuery.isSuccess && table.virtual.paddingBottom > 0 && (
                    <SpacerRow height={table.virtual.paddingBottom} colSpan={colSpan} />
                  )}
                  {table.hasNextPage && (
                    <LoadMoreRow
                      colSpan={colSpan}
                      loading={table.isFetchingNextPage}
                      onLoad={() => void table.fetchNextPage()}
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
              rows={table.virtualRows}
              props={props.visibleProps}
              visibleCount={table.visibleRows.length}
              paddingTop={table.virtual.paddingTop}
              paddingBottom={table.virtual.paddingBottom}
              isSuccess={table.objectsQuery.isSuccess}
              error={table.objectsQuery.isError ? table.objectsQuery.error : null}
              filter={table.filter}
              typeName={table.typeName}
              creating={table.create.isPending}
              hasNextPage={table.hasNextPage}
              isFetchingNextPage={table.isFetchingNextPage}
              onLoadMore={() => void table.fetchNextPage()}
              onCreate={createRow}
            />
          )}
        </div>

        {settingsOpen && (
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
  );
}
