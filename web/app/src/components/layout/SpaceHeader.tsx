import { useState } from 'react';
import { useAtomValue } from 'jotai';
import { ChevronDown, FileText, Folder, Plus, Settings2 } from 'lucide-react';
import {
  isTypeHidden,
  typeDisplayIcon,
  typeDisplayName,
  typeMetaOverridesAtom,
  useSpaceMeta,
} from '@/atoms';
import { preloadViewModule } from '@/app/viewModules';
import { CreateFolderDialog } from '@/components/objects';
import { EditSpaceDialog } from '@/components/spaces';
import {
  CreateTypeDialog,
  ListIcon,
} from '@/components/types';
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui';
import { useSpace } from '@/lib/api/spaces';
import {
  isDefaultPagesList,
  useTypes,
  visibleUserTypes,
} from '@/lib/api/types';
import { cn } from '@/lib/cn';
import { SpaceAvatar } from './SpaceAvatar';
import { useRootObjectActions } from './useSpaceContentsController';

export function SpaceHeader({ spaceId }: { spaceId: string }) {
  const spaceQuery = useSpace(spaceId);
  const typesQuery = useTypes(spaceId);
  const { createPage, createTypedObject, createFolder, isPending } =
    useRootObjectActions(spaceId);
  const [createTypeOpen, setCreateTypeOpen] = useState(false);
  const [createFolderOpen, setCreateFolderOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const { overrideName, overrideIcon } = useSpaceMeta(spaceId);
  const typeMetaOverrides = useAtomValue(typeMetaOverridesAtom);

  const effectiveName = (overrideName ?? spaceQuery.data?.name)?.trim();
  const name =
    effectiveName ||
    (spaceQuery.isPending ? 'Loading…' : `Untitled (${spaceId.slice(0, 6)}…)`);
  const userTypes = visibleUserTypes(typesQuery.data ?? []).filter(
    (t) =>
      !isDefaultPagesList(t) &&
      !isTypeHidden(t, typeMetaOverrides, spaceId),
  );

  return (
    <>
      <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-3">
        <button
          type="button"
          aria-label="Edit space"
          onClick={() => setEditOpen(true)}
          className={cn(
            'inline-flex max-w-[14rem] items-center gap-2 rounded-md px-1.5 py-1',
            'text-sm font-semibold text-foreground hover:bg-foreground/5',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <SpaceAvatar
            spaceId={spaceId}
            name={effectiveName}
            size="sm"
            iconOverride={overrideIcon}
          />
          <span className="truncate">{name}</span>
          <ChevronDown className="h-3.5 w-3.5 text-foreground/50" aria-hidden />
        </button>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              size="sm"
              variant="ghost"
              disabled={isPending}
              aria-label="New object"
            >
              <Plus className="h-3.5 w-3.5" aria-hidden />
              {isPending ? 'Creating…' : 'New'}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => void createPage()}>
              <FileText className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
              New page
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setCreateFolderOpen(true)}>
              <Folder className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
              New folder
            </DropdownMenuItem>
            {userTypes.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>Lists</DropdownMenuLabel>
                {userTypes.map((t) => {
                  const label = typeDisplayName(t, typeMetaOverrides, spaceId);
                  const icon = typeDisplayIcon(t, typeMetaOverrides, spaceId);
                  return (
                    <DropdownMenuItem
                      key={t.id}
                      onFocus={() => preloadViewModule('object')}
                      onPointerEnter={() => preloadViewModule('object')}
                      onSelect={() => void createTypedObject([t.id], label)}
                    >
                      <ListIcon icon={icon} className="h-3.5 w-3.5 text-foreground/60" />
                      New {label}
                    </DropdownMenuItem>
                  );
                })}
              </>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={() => setCreateTypeOpen(true)}>
              <Settings2 className="h-3.5 w-3.5 text-foreground/60" aria-hidden />
              Create list…
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </header>

      <CreateFolderDialog
        open={createFolderOpen}
        onOpenChange={setCreateFolderOpen}
        isPending={isPending}
        onCreate={async (name) => {
          await createFolder(name);
          setCreateFolderOpen(false);
        }}
      />
      <CreateTypeDialog open={createTypeOpen} onOpenChange={setCreateTypeOpen} />
      <EditSpaceDialog
        spaceId={editOpen ? spaceId : null}
        serverName={spaceQuery.data?.name}
        onClose={() => setEditOpen(false)}
      />
    </>
  );
}
