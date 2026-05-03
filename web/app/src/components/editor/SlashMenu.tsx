import {
  type DefaultReactSuggestionItem,
  type SuggestionMenuProps,
} from '@blocknote/react';
import { memo, useEffect, useMemo, useRef, useState } from 'react';

type SlashRow =
  | { kind: 'group'; key: string; label: string }
  | {
      kind: 'item';
      key: string;
      item: DefaultReactSuggestionItem;
      itemIndex: number;
    };

const VISIBLE_ROW_COUNT = 9;
const WHEEL_STEP_PX = 34;

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}

function buildRows(items: DefaultReactSuggestionItem[]) {
  const rows: SlashRow[] = [];
  let currentGroup: string | undefined;

  items.forEach((item, itemIndex) => {
    if (item.group !== currentGroup) {
      currentGroup = item.group;
      rows.push({
        kind: 'group',
        key: `group-${currentGroup ?? 'ungrouped'}-${itemIndex}`,
        label: currentGroup ?? '',
      });
    }

    rows.push({
      kind: 'item',
      key: `item-${item.title}-${itemIndex}`,
      item,
      itemIndex,
    });
  });

  return rows;
}

const SlashMenuItem = memo(function SlashMenuItem({
  item,
  selected,
  itemIndex,
  onPick,
}: {
  item: DefaultReactSuggestionItem;
  selected: boolean;
  itemIndex: number;
  onPick: ((item: DefaultReactSuggestionItem) => void) | undefined;
}) {
  return (
    <button
      id={`bn-suggestion-menu-item-${itemIndex}`}
      type="button"
      role="option"
      aria-selected={selected}
      className="anytype-slash-menu-item"
      onMouseDown={(event) => {
        event.preventDefault();
        event.stopPropagation();
        onPick?.(item);
      }}
    >
      <span className="anytype-slash-menu-icon" aria-hidden="true">
        {item.icon}
      </span>
      <span className="anytype-slash-menu-copy">
        <span className="anytype-slash-menu-title">{item.title}</span>
        {item.subtext ? (
          <span className="anytype-slash-menu-subtitle">{item.subtext}</span>
        ) : null}
      </span>
      {item.badge ? (
        <span className="anytype-slash-menu-badge">{item.badge}</span>
      ) : null}
    </button>
  );
});

export function AnytypeSlashMenu({
  items,
  loadingState,
  selectedIndex,
  onItemClick,
}: SuggestionMenuProps<DefaultReactSuggestionItem>) {
  const rows = useMemo(() => buildRows(items), [items]);
  const maxTopRow = Math.max(0, rows.length - VISIBLE_ROW_COUNT);
  const [topRow, setTopRow] = useState(0);
  const wheelRemainderRef = useRef(0);

  useEffect(() => {
    setTopRow(0);
    wheelRemainderRef.current = 0;
  }, [items]);

  useEffect(() => {
    if (selectedIndex === undefined) return;

    const selectedRowIndex = rows.findIndex(
      (row) => row.kind === 'item' && row.itemIndex === selectedIndex,
    );
    if (selectedRowIndex < 0) return;

    setTopRow((currentTopRow) => {
      if (selectedRowIndex < currentTopRow) {
        return selectedRowIndex;
      }

      const bottomRow = currentTopRow + VISIBLE_ROW_COUNT - 1;
      if (selectedRowIndex > bottomRow) {
        return clamp(selectedRowIndex - VISIBLE_ROW_COUNT + 1, 0, maxTopRow);
      }

      return clamp(currentTopRow, 0, maxTopRow);
    });
  }, [maxTopRow, rows, selectedIndex]);

  const visibleRows = rows.slice(topRow, topRow + VISIBLE_ROW_COUNT);
  const isLoading =
    loadingState === 'loading-initial' || loadingState === 'loading';
  const showScrollIndicator = rows.length > VISIBLE_ROW_COUNT;
  const indicatorHeight = showScrollIndicator
    ? Math.max(18, (VISIBLE_ROW_COUNT / rows.length) * 100)
    : 0;
  const indicatorTop = showScrollIndicator
    ? (topRow / Math.max(1, maxTopRow)) * (100 - indicatorHeight)
    : 0;

  return (
    <div
      id="bn-suggestion-menu"
      role="listbox"
      className="anytype-slash-menu"
      data-windowed={showScrollIndicator ? 'true' : 'false'}
      onWheel={(event) => {
        if (!showScrollIndicator) return;

        event.preventDefault();
        event.stopPropagation();

        wheelRemainderRef.current += event.deltaY;
        const rowDelta = Math.trunc(wheelRemainderRef.current / WHEEL_STEP_PX);
        if (rowDelta === 0) return;

        wheelRemainderRef.current -= rowDelta * WHEEL_STEP_PX;
        setTopRow((currentTopRow) =>
          clamp(currentTopRow + rowDelta, 0, maxTopRow),
        );
      }}
    >
      <div className="anytype-slash-menu-viewport">
        {visibleRows.map((row) =>
          row.kind === 'group' ? (
            <div className="anytype-slash-menu-label" key={row.key}>
              {row.label}
            </div>
          ) : (
            <SlashMenuItem
              key={row.key}
              item={row.item}
              itemIndex={row.itemIndex}
              selected={row.itemIndex === selectedIndex}
              onPick={onItemClick}
            />
          ),
        )}

        {items.length === 0 && !isLoading ? (
          <div className="anytype-slash-menu-empty">No commands found</div>
        ) : null}

        {isLoading ? (
          <div className="anytype-slash-menu-empty">Loading...</div>
        ) : null}
      </div>

      {showScrollIndicator ? (
        <div className="anytype-slash-menu-scrollbar" aria-hidden="true">
          <div
            className="anytype-slash-menu-scrollbar-thumb"
            style={{
              height: `${indicatorHeight}%`,
              top: `${indicatorTop}%`,
            }}
          />
        </div>
      ) : null}
    </div>
  );
}
