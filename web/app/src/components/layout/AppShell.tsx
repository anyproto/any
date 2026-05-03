import { useCallback, useEffect, useLayoutEffect, useRef } from 'react';
import { useAtom, useSetAtom } from 'jotai';
import { Panel, PanelGroup, type ImperativePanelGroupHandle } from 'react-resizable-panels';
import {
  normalizeContentsWidth,
  normalizeRailWidth,
  paneWidthsAtom,
  PANE_BOUNDS,
  spaceContentsClosedAtom,
  spacesRailClosedAtom,
  activeSpaceIdAtom,
  focusedPaneAtom,
  restoreActiveSpaceViewAtom,
  settingsOpenAtom,
  type PaneId,
} from '@/atoms';
import { useSpaces } from '@/lib/api/spaces';
import { MinWidthGuard } from './MinWidthGuard';
import { SpacesRail } from './SpacesRail';
import { SpaceContents } from './SpaceContents';
import { ObjectView } from './ObjectView';
import { ResizeHandle } from './ResizeHandle';
import { AppSettingsDialog } from '@/components/settings';

type ResizeHandleId = 'rail-contents' | 'contents-object';

/**
 * Root 3-pane application shell. PanelGroup owns the sizing; widths
 * are persisted to a Jotai atom (atomWithStorage) so reload restores
 * the user's last layout.
 *
 * Keyboard shortcuts handled here:
 *   ⌘1 / ⌘2 / ⌘3 → focus the first interactive element in pane 1/2/3.
 *
 * The min-window-width gate (800 px) wraps everything; below it the
 * shell is replaced by a centered notice (MinWidthGuard).
 */
export function AppShell() {
  const [widths, setWidths] = useAtom(paneWidthsAtom);
  const [spacesRailClosed, setSpacesRailClosed] = useAtom(spacesRailClosedAtom);
  const [spaceContentsClosed, setSpaceContentsClosed] = useAtom(spaceContentsClosedAtom);
  const [activeSpaceId, setActiveSpaceId] = useAtom(activeSpaceIdAtom);
  const restoreActiveSpaceView = useSetAtom(restoreActiveSpaceViewAtom);
  const setSettingsOpen = useSetAtom(settingsOpenAtom);
  const groupRef = useRef<HTMLDivElement>(null);
  const panelGroupRef = useRef<ImperativePanelGroupHandle>(null);
  const expandedRailWidthRef = useRef(normalizeRailWidth(widths.rail));
  const expandedContentsWidthRef = useRef(normalizeContentsWidth(widths.contents));
  const activeResizeHandleRef = useRef<ResizeHandleId | null>(null);
  const setResizeHandleDragging = useCallback((handle: ResizeHandleId, dragging: boolean) => {
    if (dragging) {
      activeResizeHandleRef.current = handle;
      return;
    }
    window.requestAnimationFrame(() => {
      if (activeResizeHandleRef.current === handle) {
        activeResizeHandleRef.current = null;
      }
    });
  }, []);
  const toggleSpacesRail = useCallback(() => {
    activeResizeHandleRef.current = null;
    setSpacesRailClosed((closed) => {
      const opening = closed;
      if (opening) {
        expandedRailWidthRef.current = PANE_BOUNDS.rail.min;
        setWidths((prev) => ({
          ...prev,
          rail: PANE_BOUNDS.rail.min,
        }));
      }
      return !closed;
    });
  }, [setSpacesRailClosed, setWidths]);
  const toggleSpaceContents = useCallback(() => {
    activeResizeHandleRef.current = null;
    setSpaceContentsClosed((closed) => {
      const opening = closed;
      if (opening) {
        expandedContentsWidthRef.current = PANE_BOUNDS.contents.min;
        setWidths((prev) => ({
          ...prev,
          contents: PANE_BOUNDS.contents.min,
        }));
      }
      return !closed;
    });
  }, [setSpaceContentsClosed, setWidths]);

  // Auto-pick logic: when /v1/spaces resolves, ensure activeSpaceId
  // points at one of the returned spaces. Two cases that need the
  // effect:
  //   - null persisted → pick the first.
  //   - persisted id no longer in the list (deleted, on another
  //     device, etc.) → pick the first remaining or null.
  const spacesQuery = useSpaces();
  useEffect(() => {
    restoreActiveSpaceView();
  }, [restoreActiveSpaceView]);

  useEffect(() => {
    if (!spacesQuery.isSuccess) return;
    const spaces = spacesQuery.data;
    const exists = activeSpaceId != null && spaces.some((s) => s.id === activeSpaceId);
    if (exists) return;
    setActiveSpaceId(spaces[0]?.id ?? null);
  }, [spacesQuery.isSuccess, spacesQuery.data, activeSpaceId, setActiveSpaceId]);

  // Move browser focus into a pane when the global shortcut fires.
  useGlobalPaneFocusShortcuts(groupRef);
  useGlobalSettingsShortcut(setSettingsOpen);

  useLayoutEffect(() => {
    const rail = spacesRailClosed ? PANE_BOUNDS.rail.closed : expandedRailWidthRef.current;
    const contents = spaceContentsClosed
      ? PANE_BOUNDS.contents.closed
      : expandedContentsWidthRef.current;
    const object = Math.max(30, 100 - rail - contents);
    panelGroupRef.current?.setLayout([rail, contents, object]);
  }, [spaceContentsClosed, spacesRailClosed]);

  return (
    <MinWidthGuard>
      <div ref={groupRef} className="relative h-screen w-screen overflow-hidden bg-background">
        <PanelGroup
          ref={panelGroupRef}
          direction="horizontal"
          autoSaveId={null}
          onLayout={(sizes) => {
            const activeHandle = activeResizeHandleRef.current;
            if (activeHandle == null) return;

            const [rail, contents] = sizes;
            setWidths((prev) => {
              const next = { ...prev };
              let changed = false;

              if (activeHandle === 'rail-contents' && !spacesRailClosed && typeof rail === 'number') {
                const nextRail = normalizeRailWidth(rail);
                expandedRailWidthRef.current = nextRail;
                next.rail = nextRail;
                changed = nextRail !== prev.rail;
              }
              if (!spaceContentsClosed && typeof contents === 'number') {
                const canResizeContents =
                  activeHandle === 'rail-contents' || activeHandle === 'contents-object';
                if (canResizeContents) {
                  const nextContents = normalizeContentsWidth(contents);
                  expandedContentsWidthRef.current = nextContents;
                  next.contents = nextContents;
                  changed = changed || nextContents !== prev.contents;
                }
              }

              return changed ? next : prev;
            });
          }}
        >
          <Panel
            id="rail"
            order={1}
            collapsible
            collapsedSize={PANE_BOUNDS.rail.closed}
            defaultSize={spacesRailClosed ? PANE_BOUNDS.rail.closed : normalizeRailWidth(widths.rail)}
            minSize={spacesRailClosed ? PANE_BOUNDS.rail.closed : PANE_BOUNDS.rail.min}
            maxSize={spacesRailClosed ? PANE_BOUNDS.rail.closed : PANE_BOUNDS.rail.max}
            className="overflow-hidden transition-[flex-grow] duration-200 ease-out"
          >
            {!spacesRailClosed && <SpacesRail />}
          </Panel>
          <ResizeHandle
            className={spacesRailClosed ? 'pointer-events-none opacity-0' : ''}
            onDragging={(dragging) => setResizeHandleDragging('rail-contents', dragging)}
          />
          <Panel
            id="contents"
            order={2}
            collapsible
            collapsedSize={PANE_BOUNDS.contents.closed}
            defaultSize={
              spaceContentsClosed ? PANE_BOUNDS.contents.closed : normalizeContentsWidth(widths.contents)
            }
            minSize={spaceContentsClosed ? PANE_BOUNDS.contents.closed : PANE_BOUNDS.contents.min}
            maxSize={spaceContentsClosed ? PANE_BOUNDS.contents.closed : PANE_BOUNDS.contents.max}
            className="overflow-hidden transition-[flex-grow] duration-200 ease-out"
          >
            {!spaceContentsClosed && <SpaceContents />}
          </Panel>
          <ResizeHandle
            className={spaceContentsClosed ? 'pointer-events-none opacity-0' : ''}
            onDragging={(dragging) => setResizeHandleDragging('contents-object', dragging)}
          />
          <Panel id="object" order={3} minSize={30}>
            <ObjectView
              spacesRailClosed={spacesRailClosed}
              spaceContentsClosed={spaceContentsClosed}
              onToggleSpacesRail={toggleSpacesRail}
              onToggleSpaceContents={toggleSpaceContents}
            />
          </Panel>
        </PanelGroup>
        <AppSettingsDialog />
      </div>
    </MinWidthGuard>
  );
}

function useGlobalSettingsShortcut(setSettingsOpen: (open: boolean) => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key !== ',') return;
      e.preventDefault();
      setSettingsOpen(true);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [setSettingsOpen]);
}

/**
 * Global ⌘1/2/3 → focus the first focusable element in the
 * matching pane. Pane scope is identified by `data-pane="1|2|3"`
 * on the container element each pane renders.
 */
function useGlobalPaneFocusShortcuts(groupRef: React.RefObject<HTMLElement | null>) {
  const [, setFocusedPane] = useAtom(focusedPaneAtom);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return;
      const paneId =
        e.key === '1' ? 1 : e.key === '2' ? 2 : e.key === '3' ? 3 : (null as PaneId | null);
      if (paneId == null) return;
      e.preventDefault();
      const root = groupRef.current ?? document;
      const pane = root.querySelector<HTMLElement>(`[data-pane="${paneId}"]`);
      if (!pane) return;
      const target = pane.querySelector<HTMLElement>(
        'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
      );
      target?.focus();
      setFocusedPane(paneId);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [groupRef, setFocusedPane]);
}
