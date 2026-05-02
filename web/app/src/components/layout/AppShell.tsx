import { useEffect, useRef } from 'react';
import { useAtom } from 'jotai';
import { Panel, PanelGroup, type ImperativePanelHandle } from 'react-resizable-panels';
import { paneWidthsAtom, PANE_BOUNDS } from '@/atoms/layout';
import { focusedPaneAtom, type PaneId } from '@/atoms/focus';
import { activeSpaceIdAtom } from '@/atoms/selection';
import { MOCK_SPACES } from '@/lib/mock-data';
import { MinWidthGuard } from './MinWidthGuard';
import { SpacesRail } from './SpacesRail';
import { SpaceContents } from './SpaceContents';
import { ObjectView } from './ObjectView';
import { ResizeHandle } from './ResizeHandle';

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
  const [activeSpaceId, setActiveSpaceId] = useAtom(activeSpaceIdAtom);
  const groupRef = useRef<HTMLDivElement>(null);

  // First-load default: pick the first mock space so pane 2 is
  // populated on first visit. PR #3 replaces this with "first space
  // from /v1/spaces" once that endpoint is wired.
  useEffect(() => {
    if (activeSpaceId == null && MOCK_SPACES.length > 0) {
      const first = MOCK_SPACES[0];
      if (first) setActiveSpaceId(first.id);
    }
  }, [activeSpaceId, setActiveSpaceId]);

  // Move browser focus into a pane when the global shortcut fires.
  useGlobalPaneFocusShortcuts(groupRef);

  return (
    <MinWidthGuard>
      <div ref={groupRef} className="h-screen w-screen overflow-hidden bg-background">
        <PanelGroup
          direction="horizontal"
          autoSaveId={null}
          onLayout={(sizes) => {
            const [rail, contents] = sizes;
            if (typeof rail === 'number' && typeof contents === 'number') {
              setWidths({ rail, contents });
            }
          }}
        >
          <Panel
            id="rail"
            order={1}
            defaultSize={widths.rail}
            minSize={PANE_BOUNDS.rail.min}
            maxSize={PANE_BOUNDS.rail.max}
          >
            <SpacesRail />
          </Panel>
          <ResizeHandle />
          <Panel
            id="contents"
            order={2}
            defaultSize={widths.contents}
            minSize={PANE_BOUNDS.contents.min}
            maxSize={PANE_BOUNDS.contents.max}
          >
            <SpaceContents />
          </Panel>
          <ResizeHandle />
          <Panel id="object" order={3} minSize={30}>
            <ObjectView />
          </Panel>
        </PanelGroup>
      </div>
    </MinWidthGuard>
  );
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

// Suppress unused-var warning until this is wired to its consumer
// in PR #3 (the focusedPaneAtom is read by SpacesRail/SpaceContents/
// ObjectView for visual focus indication).
export type { ImperativePanelHandle };
