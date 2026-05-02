import { useAtom } from 'jotai';
import { ArrowLeft, ArrowRight, ChevronRight, X } from 'lucide-react';
import { activeObjectIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { HealthCard } from '@/components/health/HealthCard';
import { useHealth } from '@/lib/api/meta';
import { cn } from '@/lib/cn';

/**
 * Pane 3 — open object.
 *
 * Empty state: hint + the existing /v1/health card (kept here until
 * a real Settings page lands).
 *
 * Selected state: shows the id + a placeholder body. The real editor
 * (BlockNote on /v1/spaces/:s/objects/:o/markdown) lands in PR #6.
 */
export function ObjectView() {
  const [activeObjectId, setActiveObjectId] = useAtom(activeObjectIdAtom);
  const setFocused = useAtom(focusedPaneAtom)[1];

  return (
    <section
      aria-label="Object view"
      data-pane="3"
      onFocus={() => setFocused(3)}
      className="flex h-full flex-col bg-background"
    >
      <Header
        objectId={activeObjectId}
        onClose={activeObjectId ? () => setActiveObjectId(null) : undefined}
      />
      <div className="flex-1 overflow-y-auto">
        {activeObjectId ? (
          <ObjectBody objectId={activeObjectId} />
        ) : (
          <EmptyState />
        )}
      </div>
    </section>
  );
}

function Header({
  objectId,
  onClose,
}: {
  objectId: string | null;
  onClose?: (() => void) | undefined;
}) {
  return (
    <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-2">
      <div className="flex items-center gap-1">
        <NavButton aria-label="Back" disabled icon={<ArrowLeft className="h-4 w-4" />} />
        <NavButton aria-label="Forward" disabled icon={<ArrowRight className="h-4 w-4" />} />
      </div>
      {objectId ? (
        <Breadcrumb segments={[`Object ${objectId.slice(0, 8)}…`]} />
      ) : (
        <div aria-hidden className="flex-1" />
      )}
      <div className="flex items-center gap-1">
        {onClose && (
          <NavButton
            aria-label="Close"
            onClick={onClose}
            icon={<X className="h-4 w-4" />}
          />
        )}
      </div>
    </header>
  );
}

function NavButton({
  icon,
  ...rest
}: {
  icon: React.ReactNode;
  'aria-label': string;
  disabled?: boolean;
  onClick?: () => void;
}) {
  return (
    <button
      type="button"
      {...rest}
      className={cn(
        'inline-flex h-7 w-7 items-center justify-center rounded-md text-foreground/60',
        'hover:bg-foreground/5 hover:text-foreground',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
        'disabled:cursor-not-allowed disabled:opacity-40',
      )}
    >
      {icon}
    </button>
  );
}

function Breadcrumb({ segments }: { segments: string[] }) {
  return (
    <nav
      aria-label="Breadcrumb"
      className="flex flex-1 items-center gap-1 overflow-hidden text-xs text-foreground/60"
    >
      {segments.map((seg, i) => {
        const last = i === segments.length - 1;
        return (
          <span key={`${seg}-${i}`} className="flex items-center gap-1">
            {i > 0 && <ChevronRight className="h-3 w-3 text-foreground/30" aria-hidden />}
            <span className={cn('truncate', last && 'font-medium text-foreground')}>
              {seg}
            </span>
          </span>
        );
      })}
    </nav>
  );
}

function ObjectBody({ objectId }: { objectId: string }) {
  return (
    <article className="mx-auto max-w-2xl px-8 py-10">
      <h1 className="mb-3 text-3xl font-semibold tracking-tight text-foreground">
        Object {objectId.slice(0, 8)}…
      </h1>
      <p className="text-sm text-foreground/60">
        Object id: <code className="font-mono">{objectId}</code>
      </p>
      <div className="mt-6 space-y-4 text-[15px] leading-7 text-foreground/85">
        <p>
          The editor lands in PR #6 — BlockNote wired to{' '}
          <code className="font-mono">GET/PUT /v1/spaces/:s/objects/:o/markdown</code>.
          Until then this pane just confirms which object is selected.
        </p>
      </div>
    </article>
  );
}

function EmptyState() {
  const health = useHealth();
  return (
    <div className="mx-auto flex max-w-xl flex-col gap-6 p-8">
      <p className="text-sm text-foreground/60">
        Pick an item from the sidebar to view it. Until PR #3+ lands a
        real Settings page, the server health card is shown here for
        convenience.
      </p>
      <HealthCard query={health} />
    </div>
  );
}
