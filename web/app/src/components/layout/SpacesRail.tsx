import { useAtom } from 'jotai';
import { Plus, Settings } from 'lucide-react';
import { activeSpaceIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { MOCK_SPACES, type MockSpace } from '@/lib/mock-data';
import { SimpleTooltip } from '@/components/ui/Tooltip';
import { cn } from '@/lib/cn';

const TONE_BG: Record<MockSpace['tone'], string> = {
  accent: 'bg-accent text-background',
  success: 'bg-success text-background',
  info: 'bg-info text-background',
  destructive: 'bg-destructive text-background',
  foreground: 'bg-foreground/80 text-background',
};

/**
 * Pane 1 — vertical strip of spaces.
 *
 * Active space = ring in --color-accent. Click to switch. Plus button
 * is a no-op in PR #2 (PR #3 wires create); settings cog is a no-op
 * (PR #?). Placeholders so the visual is complete.
 */
export function SpacesRail() {
  const [activeId, setActiveId] = useAtom(activeSpaceIdAtom);
  const [, setFocused] = useAtom(focusedPaneAtom);

  return (
    <nav
      aria-label="Spaces"
      data-pane="1"
      onFocus={() => setFocused(1)}
      className="flex h-full flex-col items-center justify-between bg-foreground/[0.03] py-3"
    >
      <ul className="flex flex-col items-center gap-2 overflow-y-auto">
        {MOCK_SPACES.map((space) => {
          const active = space.id === activeId;
          return (
            <li key={space.id}>
              <SimpleTooltip text={space.name} side="right">
                <button
                  type="button"
                  aria-label={space.name}
                  aria-current={active ? 'page' : undefined}
                  onClick={() => setActiveId(space.id)}
                  className={cn(
                    'flex h-9 w-9 items-center justify-center rounded-lg text-sm font-semibold',
                    'transition-all',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background',
                    TONE_BG[space.tone],
                    active && 'ring-2 ring-accent ring-offset-2 ring-offset-background',
                  )}
                >
                  {space.glyph}
                </button>
              </SimpleTooltip>
            </li>
          );
        })}
        <li>
          <SimpleTooltip text="New space (PR #3)" side="right">
            <button
              type="button"
              aria-label="New space"
              disabled
              className={cn(
                'flex h-9 w-9 items-center justify-center rounded-lg',
                'border border-dashed border-foreground/20 text-foreground/40',
                'hover:border-foreground/40 hover:text-foreground/70 disabled:cursor-not-allowed',
              )}
            >
              <Plus className="h-4 w-4" aria-hidden />
            </button>
          </SimpleTooltip>
        </li>
      </ul>
      <SimpleTooltip text="Settings" side="right">
        <button
          type="button"
          aria-label="Settings"
          disabled
          className={cn(
            'flex h-8 w-8 items-center justify-center rounded-md text-foreground/50',
            'hover:bg-foreground/5 hover:text-foreground',
            'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
          )}
        >
          <Settings className="h-4 w-4" aria-hidden />
        </button>
      </SimpleTooltip>
    </nav>
  );
}
