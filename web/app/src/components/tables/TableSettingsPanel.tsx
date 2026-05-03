import { useState } from 'react';
import type { TableViewLayout } from '@/atoms';
import type { PropertyDef } from '@/lib/api/types';
import { Input } from '@/components/ui';
import { sortSummary, type SortDir } from './tableSorting';
import { AddPropertyScreen } from './settings/AddPropertyScreen';
import { FilterScreen } from './settings/FilterScreen';
import { LayoutScreen } from './settings/LayoutScreen';
import { PropertiesScreen } from './settings/PropertiesScreen';
import { PropertyVisibilityScreen } from './settings/PropertyVisibilityScreen';
import { SortScreen } from './settings/SortScreen';
import {
  EditPropertyScreen,
  PanelHeader,
  PlaceholderScreen,
  SettingRowButton,
} from './settings/settingsPrimitives';

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

export function TableSettingsPanel({
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
  const hiddenCount = hiddenPropIds.size;
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
        <PropertyVisibilityScreen
          props={props}
          hiddenPropIds={hiddenPropIds}
          onToggleProperty={onToggleProperty}
          onMoveProperty={onMoveProperty}
        />
      )}

      {screen === 'filter' && (
        <FilterScreen filter={filter} onFilterChange={onFilterChange} />
      )}

      {screen === 'sort' && (
        <SortScreen
          props={props}
          typeId={typeId}
          sortKey={sortKey}
          sortDir={sortDir}
          onSort={onSort}
        />
      )}

      {screen === 'layout' && (
        <LayoutScreen
          viewLayout={viewLayout}
          onViewLayoutChange={onViewLayoutChange}
        />
      )}

      {screen === 'properties' && (
        <PropertiesScreen
          props={props}
          onAddProperty={() => setScreen('add-property')}
          onSelectProperty={(propId) => {
            setSelectedPropId(propId);
            setScreen('edit-property');
          }}
        />
      )}

      {screen === 'add-property' && (
        <AddPropertyScreen
          spaceId={spaceId}
          typeId={typeId}
          onCreated={() => setScreen('properties')}
        />
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
