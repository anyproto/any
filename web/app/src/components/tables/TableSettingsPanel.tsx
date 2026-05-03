import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
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
  | 'properties'
  | 'add-property'
  | 'edit-property';

type ScreenDirection = 'forward' | 'back';

const SCREEN_TRANSITION_MS = 180;

const SCREEN_TITLES: Record<SettingsScreen, string> = {
  main: 'View settings',
  layout: 'Layout',
  'property-visibility': 'Property visibility',
  filter: 'Filter',
  sort: 'Sort',
  properties: 'Properties',
  'add-property': 'Add property',
  'edit-property': 'Edit property',
};

const LAYOUT_LABELS: Record<TableViewLayout, string> = {
  table: 'Table',
  list: 'List',
  gallery: 'Gallery',
};

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
  const [transition, setTransition] = useState<{
    from: SettingsScreen;
    direction: ScreenDirection;
  } | null>(null);
  const screenRef = useRef<SettingsScreen>('main');
  const transitionTimerRef = useRef<number | null>(null);
  const hiddenCount = hiddenPropIds.size;
  const selectedProp = props.find((p) => p.id === selectedPropId) ?? null;

  const navigate = useCallback((next: SettingsScreen, direction: ScreenDirection) => {
    const from = screenRef.current;
    if (from === next) return;

    if (transitionTimerRef.current != null) {
      window.clearTimeout(transitionTimerRef.current);
    }

    screenRef.current = next;
    setTransition({ from, direction });
    setScreen(next);
    transitionTimerRef.current = window.setTimeout(() => {
      setTransition(null);
      transitionTimerRef.current = null;
    }, SCREEN_TRANSITION_MS);
  }, []);

  useEffect(() => {
    return () => {
      if (transitionTimerRef.current != null) {
        window.clearTimeout(transitionTimerRef.current);
      }
    };
  }, []);

  const openScreen = useCallback(
    (next: SettingsScreen) => navigate(next, 'forward'),
    [navigate],
  );
  const goBack = useCallback(() => navigate('main', 'back'), [navigate]);

  const renderScreenBody = (targetScreen: SettingsScreen): ReactNode => {
    if (targetScreen === 'main') {
      return (
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
              value={LAYOUT_LABELS[viewLayout]}
              onClick={() => openScreen('layout')}
            />
            <SettingRowButton
              label="Property visibility"
              value={hiddenCount === 0 ? `${props.length}` : `${hiddenCount} hidden`}
              onClick={() => openScreen('property-visibility')}
            />
            <SettingRowButton
              label="Filter"
              value={filter.trim() === '' ? 'None' : filter.trim()}
              onClick={() => openScreen('filter')}
            />
            <SettingRowButton
              label="Sort"
              value={sortSummary(props, typeId, sortKey, sortDir)}
              onClick={() => openScreen('sort')}
            />
          </div>

          <div className="my-4 h-px bg-foreground/[0.08]" />

          <p className="mb-2 px-2 text-xs font-medium text-foreground/45">
            Collection settings
          </p>
          <div className="space-y-1">
            <SettingRowButton
              label="Properties"
              value={`${props.length}`}
              onClick={() => openScreen('properties')}
            />
          </div>
        </>
      );
    }

    if (targetScreen === 'property-visibility') {
      return (
        <PropertyVisibilityScreen
          props={props}
          hiddenPropIds={hiddenPropIds}
          onToggleProperty={onToggleProperty}
          onMoveProperty={onMoveProperty}
        />
      );
    }

    if (targetScreen === 'filter') {
      return <FilterScreen filter={filter} onFilterChange={onFilterChange} />;
    }

    if (targetScreen === 'sort') {
      return (
        <SortScreen
          props={props}
          typeId={typeId}
          sortKey={sortKey}
          sortDir={sortDir}
          onSort={onSort}
        />
      );
    }

    if (targetScreen === 'layout') {
      return (
        <LayoutScreen
          viewLayout={viewLayout}
          onViewLayoutChange={onViewLayoutChange}
        />
      );
    }

    if (targetScreen === 'properties') {
      return (
        <PropertiesScreen
          props={props}
          onAddProperty={() => openScreen('add-property')}
          onSelectProperty={(propId) => {
            setSelectedPropId(propId);
            openScreen('edit-property');
          }}
        />
      );
    }

    if (targetScreen === 'add-property') {
      return (
        <AddPropertyScreen
          spaceId={spaceId}
          typeId={typeId}
          onCreated={() => navigate('properties', 'back')}
        />
      );
    }

    if (targetScreen === 'edit-property') {
      return selectedProp ? (
        <EditPropertyScreen prop={selectedProp} />
      ) : (
        <PlaceholderScreen
          title="Property not found"
          body="The property list changed. Go back and select it again."
        />
      );
    }

    return null;
  };

  const renderScreen = (targetScreen: SettingsScreen) => (
    <>
      <PanelHeader
        title={SCREEN_TITLES[targetScreen]}
        subtitle={targetScreen === 'main' ? 'Local table controls' : undefined}
        onBack={targetScreen === 'main' ? undefined : goBack}
        onClose={onClose}
      />
      {renderScreenBody(targetScreen)}
    </>
  );

  return (
    <aside
      aria-label="View settings"
      className="h-full w-[22rem] shrink-0 overflow-hidden border-l border-foreground/[0.08] bg-background"
    >
      <div className="relative h-full overflow-hidden">
        {transition && (
          <div
            key={`${transition.from}-previous`}
            aria-hidden
            className={screenPaneClass('previous', transition.direction)}
          >
            {renderScreen(transition.from)}
          </div>
        )}
        <div
          key={`${screen}-current`}
          className={
            transition
              ? screenPaneClass('current', transition.direction)
              : screenPaneBaseClass
          }
        >
          {renderScreen(screen)}
        </div>
      </div>
    </aside>
  );
}

const screenPaneBaseClass =
  'settings-screen-pane absolute inset-0 overflow-y-auto px-5 py-4';

function screenPaneClass(role: 'current' | 'previous', direction: ScreenDirection) {
  if (role === 'current') {
    return `${screenPaneBaseClass} z-10 ${
      direction === 'forward'
        ? 'settings-screen-enter-forward'
        : 'settings-screen-enter-back'
    }`;
  }

  return `${screenPaneBaseClass} pointer-events-none z-0 ${
    direction === 'forward'
      ? 'settings-screen-exit-forward'
      : 'settings-screen-exit-back'
  }`;
}
