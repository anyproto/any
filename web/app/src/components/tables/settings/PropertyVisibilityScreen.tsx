import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import { Eye, EyeOff, GripVertical } from 'lucide-react';
import type { PropertyDef } from '@/lib/api/types';
import { cn } from '@/lib/cn';
import { propertyDisplayName } from './settingsPrimitives';

const PROPERTY_VISIBILITY_ROW_HEIGHT = 32;
const PROPERTY_VISIBILITY_ROW_GAP = 4;
const PROPERTY_VISIBILITY_ROW_STEP =
  PROPERTY_VISIBILITY_ROW_HEIGHT + PROPERTY_VISIBILITY_ROW_GAP;

interface PropertyVisibilityDragState {
  propId: string;
  pointerId: number;
  startY: number;
  currentY: number;
  targetId: string;
}

export function PropertyVisibilityScreen({
  props,
  hiddenPropIds,
  onToggleProperty,
  onMoveProperty,
}: {
  props: PropertyDef[];
  hiddenPropIds: Set<string>;
  onToggleProperty: (propId: string, visible: boolean) => void;
  onMoveProperty: (sourceId: string, targetId: string) => void;
}) {
  const shownProps = props.filter((p) => !hiddenPropIds.has(p.id));
  const hiddenProps = props.filter((p) => hiddenPropIds.has(p.id));

  return (
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
  );
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
          name={propertyDisplayName(p)}
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
