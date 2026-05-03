import { HierarchySection } from './HierarchySection';
import { ListsSection } from './ListsSection';
import { SpaceHeader } from './SpaceHeader';
import { useSpaceContentsController } from './useSpaceContentsController';

/**
 * Pane 2 — current space contents.
 *
 * Header: small space avatar + name + chevron + actions.
 * Body: two sections — Pages (the tree, expanded) and Types (collapsed).
 */
export function SpaceContents() {
  const {
    activeSpaceId,
    focusPane,
    hierarchyOpen,
    toggleHierarchy,
    listsOpen,
    toggleLists,
  } = useSpaceContentsController();

  if (!activeSpaceId) {
    return (
      <section
        data-pane="2"
        onFocus={focusPane}
        className="flex h-full items-center justify-center p-4"
      >
        <p className="text-sm text-foreground/60">Pick a space on the left.</p>
      </section>
    );
  }

  return (
    <section
      aria-label="Space contents"
      data-pane="2"
      onFocus={focusPane}
      className="flex h-full flex-col bg-foreground/[0.02]"
    >
      <SpaceHeader spaceId={activeSpaceId} />
      <div className="flex-1 overflow-y-auto pb-3">
        <div className="mt-2">
          <HierarchySection
            spaceId={activeSpaceId}
            expanded={hierarchyOpen}
            onToggle={toggleHierarchy}
          />
        </div>
        <div className="mt-3">
          <ListsSection
            spaceId={activeSpaceId}
            expanded={listsOpen}
            onToggle={toggleLists}
          />
        </div>
      </div>
    </section>
  );
}
