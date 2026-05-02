import { useAtom } from 'jotai';
import { ArrowLeft, ArrowRight, ChevronRight, X } from 'lucide-react';
import { activeObjectIdAtom } from '@/atoms/selection';
import { focusedPaneAtom } from '@/atoms/focus';
import { mockObjectById } from '@/lib/mock-data';
import { HealthCard } from '@/components/health/HealthCard';
import { useHealth } from '@/lib/api/meta';
import { cn } from '@/lib/cn';

/**
 * Pane 3 — open object.
 *
 * Empty state (nothing selected): a friendly hint, plus the existing
 * /v1/health card so the dev surface is still visible (deleted in
 * PR #3+ when a real Settings surface exists).
 *
 * Selected state: breadcrumb + back/forward + mock title and body.
 */
export function ObjectView() {
  const [activeObjectId, setActiveObjectId] = useAtom(activeObjectIdAtom);
  const setFocused = useAtom(focusedPaneAtom)[1];
  const obj = mockObjectById(activeObjectId);

  return (
    <section
      aria-label="Object view"
      data-pane="3"
      onFocus={() => setFocused(3)}
      className="flex h-full flex-col bg-background"
    >
      <Header
        breadcrumb={obj?.breadcrumb}
        onClose={obj ? () => setActiveObjectId(null) : undefined}
      />
      <div className="flex-1 overflow-y-auto">
        {obj ? <ObjectBody title={obj.title} body={obj.body} /> : <EmptyState />}
      </div>
    </section>
  );
}

function Header({
  breadcrumb,
  onClose,
}: {
  breadcrumb?: string[] | undefined;
  onClose?: (() => void) | undefined;
}) {
  return (
    <header className="flex items-center justify-between gap-2 border-b border-foreground/[0.06] px-3 py-2">
      <div className="flex items-center gap-1">
        <NavButton aria-label="Back" disabled icon={<ArrowLeft className="h-4 w-4" />} />
        <NavButton aria-label="Forward" disabled icon={<ArrowRight className="h-4 w-4" />} />
      </div>
      <Breadcrumb segments={breadcrumb ?? []} />
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
  if (segments.length === 0) return <div aria-hidden className="flex-1" />;
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

function ObjectBody({ title, body }: { title: string; body: string[] }) {
  return (
    <article className="mx-auto max-w-2xl px-8 py-10">
      <h1 className="mb-6 text-3xl font-semibold tracking-tight text-foreground">{title}</h1>
      <div className="space-y-4 text-[15px] leading-7 text-foreground/85">
        {body.map((p, i) => (
          <p key={i}>{p}</p>
        ))}
      </div>
    </article>
  );
}

function EmptyState() {
  const health = useHealth();
  return (
    <div className="mx-auto flex max-w-xl flex-col gap-6 p-8">
      <p className="text-sm text-foreground/60">
        Pick an item from the sidebar to view it. Until PR #3 lands a real Settings page,
        the server health is shown here for convenience.
      </p>
      <HealthCard query={health} />
    </div>
  );
}
