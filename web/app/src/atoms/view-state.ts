import { atom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';
import type { Getter, Setter } from 'jotai/vanilla/typeUtils';
import { appStorage } from '@/shared';

/**
 * What's open in pane 3. Discriminated union so an object and a type
 * can never both claim the pane at once.
 */
export type ActiveView =
  | { kind: 'empty' }
  | { kind: 'object'; objectId: string; typeId?: string | null }
  | { kind: 'type-table'; typeId: string };

export type PaneId = 1 | 2 | 3;

/**
 * Anchor for shift+range selection - the row that started the
 * current selection. parentId stays alongside the id so we can
 * reject cross-parent shift+clicks (we only support same-parent
 * ranges in v1).
 */
export interface TreeSelectionAnchor {
  id: string;
  parentId: string;
}

export interface TreeSelectionState {
  ids: ReadonlySet<string>;
  anchor: TreeSelectionAnchor | null;
  keyboardEdgeId: string | null;
}

export interface ViewState {
  activeSpaceId: string | null;
  activeView: ActiveView;
  focusedPane: PaneId;
  treeSelection: TreeSelectionState;
  renamingObjectId: string | null;
  pendingBulkDelete: readonly string[] | null;
}

const EMPTY_VIEW: ActiveView = { kind: 'empty' };
const MAX_NAV_HISTORY = 100;

interface NavEntry {
  spaceId: string | null;
  view: ActiveView;
}

interface NavigationState {
  past: NavEntry[];
  future: NavEntry[];
}

/**
 * Active space - the one whose contents are shown in pane 2.
 *
 * Persisted so reload restores the user's view. The exported atom
 * below wraps this base atom with pane-3 view restore + navigation
 * history behavior.
 */
const activeSpaceIdBaseAtom = atomWithStorage<string | null>(
  'any.selection.activeSpace.v1',
  null,
  appStorage<string | null>(),
  { getOnInit: true },
);

/**
 * Raw pane-3 view. Keep writes routed through activeViewAtom so history
 * and per-space restore stay coherent.
 */
const activeViewBaseAtom = atom<ActiveView>(EMPTY_VIEW);

const lastViewBySpaceAtom = atomWithStorage<Record<string, ActiveView>>(
  'any.selection.lastViewBySpace.v1',
  {},
  appStorage<Record<string, ActiveView>>(),
  { getOnInit: true },
);

export const lastViewBySpacePreviewAtom = atom((get) => get(lastViewBySpaceAtom));

const navHistoryAtom = atom<NavigationState>({ past: [], future: [] });

/**
 * Which pane currently owns keyboard focus.
 * Updated by the global Cmd+1 / Cmd+2 / Cmd+3 shortcuts and by focusin events.
 *
 * NOTE: this is a hint for visual indication and shortcuts. It does
 * not move browser focus on its own - see useFocusPane() in App.tsx.
 */
export const focusedPaneAtom = atom<PaneId>(2);

/**
 * Multi-selection state for the pages tree. Shift+click range
 * selection + Cmd/Ctrl+click toggle. See
 * docs/specs/PR-024-tree-multi-select.md.
 *
 * ids, anchor, and keyboard edge stay as separate atoms so rows can
 * subscribe to cheap per-slice selectors while the aggregate
 * viewStateAtom still gives agents one place to inspect UI state.
 */
export const selectedTreeIdsAtom = atom<Set<string>>(new Set<string>());
export const treeSelectionAnchorAtom = atom<TreeSelectionAnchor | null>(null);

/**
 * Moving edge for Shift+Arrow range selection. Keyboard events can be
 * delivered faster than focus paints in Chromium, so relying only on
 * `document.activeElement` makes reverse direction flaky. This atom is
 * the file-manager-style focused edge of the range.
 */
export const treeSelectionKeyboardEdgeAtom = atom<string | null>(null);

/** Convenience write-only atom: clears selected ids, anchor, and keyboard edge. */
export const clearTreeSelectionAtom = atom(null, (_get, set) => {
  set(selectedTreeIdsAtom, new Set<string>());
  set(treeSelectionAnchorAtom, null);
  set(treeSelectionKeyboardEdgeAtom, null);
});

/**
 * When non-null, the BulkDeleteObjectsDialog is shown for these ids.
 * A row sets this when Delete is pressed and the current row is in
 * a multi-selection of at least 2 items; ObjectTree renders the dialog.
 */
export const pendingBulkDeleteAtom = atom<string[] | null>(null);

/**
 * The id of the object currently being renamed inline. Only one row
 * is in edit mode at a time. Set by ObjectRow's rename trigger;
 * cleared when the input commits or cancels.
 */
export const renamingObjectIdAtom = atom<string | null>(null);

export const viewStateAtom = atom(
  (get): ViewState => ({
    activeSpaceId: get(activeSpaceIdAtom),
    activeView: get(activeViewAtom),
    focusedPane: get(focusedPaneAtom),
    treeSelection: {
      ids: get(selectedTreeIdsAtom),
      anchor: get(treeSelectionAnchorAtom),
      keyboardEdgeId: get(treeSelectionKeyboardEdgeAtom),
    },
    renamingObjectId: get(renamingObjectIdAtom),
    pendingBulkDelete: get(pendingBulkDeleteAtom),
  }),
);

function isActiveView(value: unknown): value is ActiveView {
  if (value === null || typeof value !== 'object') return false;
  const maybe = value as Partial<ActiveView>;
  if (maybe.kind === 'empty') return true;
  if (maybe.kind === 'object') {
    const objectView = maybe as { objectId?: unknown; typeId?: unknown };
    return (
      typeof objectView.objectId === 'string' &&
      (objectView.typeId === undefined ||
        objectView.typeId === null ||
        typeof objectView.typeId === 'string')
    );
  }
  if (maybe.kind === 'type-table') {
    return typeof (maybe as { typeId?: unknown }).typeId === 'string';
  }
  return false;
}

function normalizeStoredView(value: unknown): ActiveView {
  return isActiveView(value) ? value : EMPTY_VIEW;
}

function sameView(a: ActiveView, b: ActiveView): boolean {
  if (a.kind !== b.kind) return false;
  if (a.kind === 'object') {
    const other = b as typeof a;
    return a.objectId === other.objectId && (a.typeId ?? null) === (other.typeId ?? null);
  }
  if (a.kind === 'type-table') return a.typeId === (b as typeof a).typeId;
  return true;
}

function sameEntry(a: NavEntry, b: NavEntry): boolean {
  return a.spaceId === b.spaceId && sameView(a.view, b.view);
}

function currentEntry(get: Getter): NavEntry {
  return {
    spaceId: get(activeSpaceIdBaseAtom),
    view: get(activeViewBaseAtom),
  };
}

function isColdStartEntry(entry: NavEntry): boolean {
  return entry.spaceId == null && entry.view.kind === 'empty';
}

function lastViewForSpace(get: Getter, spaceId: string | null): ActiveView {
  if (spaceId == null) return EMPTY_VIEW;
  return normalizeStoredView(get(lastViewBySpaceAtom)[spaceId]);
}

function rememberViewForSpace(
  get: Getter,
  set: Setter,
  spaceId: string | null,
  view: ActiveView,
) {
  if (spaceId == null) return;
  const current = get(lastViewBySpaceAtom);
  if (sameView(normalizeStoredView(current[spaceId]), view)) return;
  set(lastViewBySpaceAtom, { ...current, [spaceId]: view });
}

function pushCurrentToHistory(get: Getter, set: Setter, next: NavEntry) {
  const current = currentEntry(get);
  if (sameEntry(current, next)) return;

  const history = get(navHistoryAtom);
  if (
    history.past.length === 0 &&
    history.future.length === 0 &&
    isColdStartEntry(current)
  ) {
    return;
  }

  set(navHistoryAtom, {
    past: [...history.past, current].slice(-MAX_NAV_HISTORY),
    future: [],
  });
}

function applyNavigationEntry(get: Getter, set: Setter, entry: NavEntry) {
  set(activeSpaceIdBaseAtom, entry.spaceId);
  set(activeViewBaseAtom, entry.view);
  rememberViewForSpace(get, set, entry.spaceId, entry.view);
}

export const activeSpaceIdAtom = atom(
  (get) => get(activeSpaceIdBaseAtom),
  (get, set, nextSpaceId: string | null) => {
    const current = currentEntry(get);
    if (current.spaceId === nextSpaceId) {
      set(activeSpaceIdBaseAtom, nextSpaceId);
      return;
    }

    rememberViewForSpace(get, set, current.spaceId, current.view);
    const next: NavEntry = {
      spaceId: nextSpaceId,
      view: lastViewForSpace(get, nextSpaceId),
    };

    pushCurrentToHistory(get, set, next);
    set(activeSpaceIdBaseAtom, next.spaceId);
    set(activeViewBaseAtom, next.view);
  },
);

export const activeViewAtom = atom(
  (get) => get(activeViewBaseAtom),
  (get, set, nextView: ActiveView) => {
    const current = currentEntry(get);
    if (sameView(current.view, nextView)) return;

    const next: NavEntry = {
      spaceId: current.spaceId,
      view: nextView,
    };

    pushCurrentToHistory(get, set, next);
    set(activeViewBaseAtom, nextView);
    rememberViewForSpace(get, set, current.spaceId, nextView);
  },
);

export const canGoBackAtom = atom((get) => get(navHistoryAtom).past.length > 0);
export const canGoForwardAtom = atom((get) => get(navHistoryAtom).future.length > 0);

export const goBackAtom = atom(null, (get, set) => {
  const history = get(navHistoryAtom);
  const previous = history.past[history.past.length - 1];
  if (!previous) return;

  const current = currentEntry(get);
  set(navHistoryAtom, {
    past: history.past.slice(0, -1),
    future: [current, ...history.future].slice(0, MAX_NAV_HISTORY),
  });
  applyNavigationEntry(get, set, previous);
});

export const goForwardAtom = atom(null, (get, set) => {
  const history = get(navHistoryAtom);
  const next = history.future[0];
  if (!next) return;

  const current = currentEntry(get);
  set(navHistoryAtom, {
    past: [...history.past, current].slice(-MAX_NAV_HISTORY),
    future: history.future.slice(1),
  });
  applyNavigationEntry(get, set, next);
});

/**
 * App boot reads activeSpaceId from storage, but activeView starts
 * in-memory. This hydrates pane 3 from the per-space cache without
 * creating a Back entry.
 */
export const restoreActiveSpaceViewAtom = atom(null, (get, set) => {
  const current = currentEntry(get);
  if (current.spaceId == null || current.view.kind !== 'empty') return;

  const restored = lastViewForSpace(get, current.spaceId);
  if (restored.kind === 'empty') return;
  set(activeViewBaseAtom, restored);
});

/**
 * Back-compat read/write derived atom - every existing call site that
 * read or wrote `activeObjectIdAtom` keeps working unchanged.
 *
 * Reads: returns the object id when view.kind === 'object', else null.
 * Writes: setting null -> empty; setting a string -> kind: 'object'.
 */
export const activeObjectIdAtom = atom(
  (get): string | null => {
    const v = get(activeViewAtom);
    return v.kind === 'object' ? v.objectId : null;
  },
  (_get, set, value: string | null) => {
    if (value == null) set(activeViewAtom, { kind: 'empty' });
    else set(activeViewAtom, { kind: 'object', objectId: value });
  },
);

/**
 * Open an object while preserving the type/list surface it came from.
 * The legacy activeObjectIdAtom intentionally does not set this so
 * hierarchy, relation, and generic object navigation remain plain.
 */
export const openObjectFromTypeAtom = atom(
  null,
  (_get, set, args: { objectId: string; typeId: string | null }) => {
    const typeId = args.typeId?.trim();
    set(
      activeViewAtom,
      typeId
        ? { kind: 'object', objectId: args.objectId, typeId }
        : { kind: 'object', objectId: args.objectId },
    );
  },
);

/**
 * Read/write derived atom for the type-table side. Mirror shape.
 */
export const activeTypeIdAtom = atom(
  (get): string | null => {
    const v = get(activeViewAtom);
    return v.kind === 'type-table' ? v.typeId : null;
  },
  (_get, set, value: string | null) => {
    if (value == null) set(activeViewAtom, { kind: 'empty' });
    else set(activeViewAtom, { kind: 'type-table', typeId: value });
  },
);

export type TreeExpansionAction = 'expand' | 'collapse';

export interface TreeExpansionSignal {
  spaceId: string;
  action: TreeExpansionAction;
  nonce: number;
}

/**
 * Broadcast-only signal for "expand all folders" / "collapse all
 * folders" from the space section header. Rows keep local expanded
 * state for cheap normal toggles, and only react when this nonce
 * changes.
 */
export const treeExpansionSignalAtom = atom<TreeExpansionSignal | null>(null);

export const sendTreeExpansionSignalAtom = atom(
  null,
  (get, set, args: { spaceId: string; action: TreeExpansionAction }) => {
    const current = get(treeExpansionSignalAtom);
    set(treeExpansionSignalAtom, {
      ...args,
      nonce: (current?.nonce ?? 0) + 1,
    });
  },
);

/**
 * Compute the inclusive range of ids between two siblings, sorted by
 * `nav.pos` (the same order the tree renders them in).
 *
 * `siblings` is the cached children-of-parent list. `fromId` is the
 * anchor; `toId` is the row just clicked. Returns the list of ids
 * spanning the two, in render order.
 */
export function rangeIds(
  siblings: { id: string; nav?: { pos?: string } }[],
  fromId: string,
  toId: string,
): string[] {
  const sorted = [...siblings].sort((a, b) =>
    (a.nav?.pos ?? '') < (b.nav?.pos ?? '') ? -1 : 1,
  );
  const fi = sorted.findIndex((s) => s.id === fromId);
  const ti = sorted.findIndex((s) => s.id === toId);
  if (fi === -1 || ti === -1) return [toId];
  const [start, end] = fi < ti ? [fi, ti] : [ti, fi];
  return sorted.slice(start, end + 1).map((s) => s.id);
}
