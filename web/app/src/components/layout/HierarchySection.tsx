import { useState, type ReactNode } from 'react';
import { useSetAtom } from 'jotai';
import {
  ChevronsDownUp,
  ChevronsUpDown,
  FolderPlus,
  Plus,
} from 'lucide-react';
import { sendTreeExpansionSignalAtom, useSpaceMeta } from '@/atoms';
import { CreateFolderDialog } from '@/components/objects';
import { ObjectTree } from '@/components/tree';
import { useSpace } from '@/lib/api/spaces';
import { cn } from '@/lib/cn';
import { SectionHeader } from './SectionHeader';
import { useRootObjectActions } from './useSpaceContentsController';

export function HierarchySection({
  spaceId,
  expanded,
  onToggle,
}: {
  spaceId: string;
  expanded: boolean;
  onToggle: () => void;
}) {
  const spaceQuery = useSpace(spaceId);
  const { overrideName } = useSpaceMeta(spaceId);
  const { createPage, createFolder, isPending } = useRootObjectActions(spaceId);
  const sendTreeExpansionSignal = useSetAtom(sendTreeExpansionSignalAtom);
  const [foldersExpanded, setFoldersExpanded] = useState(false);
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const label =
    (overrideName ?? spaceQuery.data?.name)?.trim() ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);

  const toggleAllFolders = () => {
    const nextExpanded = !foldersExpanded;
    setFoldersExpanded(nextExpanded);
    if (!expanded) onToggle();
    sendTreeExpansionSignal({
      spaceId,
      action: nextExpanded ? 'expand' : 'collapse',
    });
  };

  return (
    <>
      <SectionHeader
        label={label}
        expanded={expanded}
        onToggle={onToggle}
        action={
          <div className="flex items-center gap-0.5 opacity-70 transition-opacity group-hover/section:opacity-100 focus-within:opacity-100">
            <SectionIconButton
              label="New page"
              title="New page"
              disabled={isPending}
              onClick={() => void createPage()}
            >
              <Plus className="h-3 w-3" aria-hidden />
            </SectionIconButton>
            <SectionIconButton
              label="New folder"
              title="New folder"
              disabled={isPending}
              onClick={() => setCreateFolderOpen(true)}
            >
              <FolderPlus className="h-3 w-3" aria-hidden />
            </SectionIconButton>
            <SectionIconButton
              label={foldersExpanded ? 'Collapse all folders' : 'Expand all folders'}
              title={foldersExpanded ? 'Collapse all folders' : 'Expand all folders'}
              onClick={toggleAllFolders}
            >
              {foldersExpanded ? (
                <ChevronsDownUp className="h-3 w-3" aria-hidden />
              ) : (
                <ChevronsUpDown className="h-3 w-3" aria-hidden />
              )}
            </SectionIconButton>
          </div>
        }
      />
      <CreateFolderDialog
        open={createFolderOpen}
        onOpenChange={setCreateFolderOpen}
        isPending={isPending}
        onCreate={async (name) => {
          await createFolder(name);
          setCreateFolderOpen(false);
        }}
      />
      {expanded && <ObjectTree spaceId={spaceId} />}
    </>
  );
}

function SectionIconButton({
  label,
  title,
  disabled,
  onClick,
  children,
}: {
  label: string;
  title: string;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={title}
      disabled={disabled}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className={cn(
        'inline-flex h-5 w-5 items-center justify-center rounded text-foreground/45',
        'hover:bg-foreground/7 hover:text-foreground/75 disabled:cursor-not-allowed disabled:opacity-40',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
      )}
    >
      {children}
    </button>
  );
}
