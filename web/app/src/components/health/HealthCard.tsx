import type { UseQueryResult } from '@tanstack/react-query';
import { CircleAlert, CircleCheck, CircleDashed } from 'lucide-react';
import type { HealthResponse } from '@/lib/api/meta';
import { ApiError } from '@/lib/api/client';
import { cn } from '@/lib/cn';

interface HealthCardProps {
  query: UseQueryResult<HealthResponse>;
}

/**
 * Three-state card for the /v1/health response. Pure presentation —
 * the query is injected so tests can mock it without React Query.
 */
export function HealthCard({ query }: HealthCardProps) {
  if (query.isPending) return <Loading />;
  if (query.isError) return <ErrorState error={query.error as unknown} />;
  return <Ok data={query.data} />;
}

function CardShell({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section
      aria-labelledby="health-heading"
      className={cn(
        'rounded-lg border border-foreground/10 bg-background p-6 shadow-sm',
        className,
      )}
    >
      {children}
    </section>
  );
}

function Loading() {
  return (
    <CardShell>
      <Header
        icon={<CircleDashed className="h-4 w-4 animate-spin text-foreground/50" aria-hidden />}
        label="Checking…"
      />
      <Rows>
        {[1, 2, 3].map((i) => (
          <Row key={i} label={<Skeleton className="w-20" />} value={<Skeleton className="w-44" />} />
        ))}
      </Rows>
    </CardShell>
  );
}

function ErrorState({ error }: { error: unknown }) {
  const code = error instanceof ApiError ? error.code : 'unknown';
  const message =
    error instanceof Error
      ? error.message
      : typeof error === 'string'
        ? error
        : 'Could not reach /v1/health';
  return (
    <CardShell className="border-destructive/40">
      <Header
        icon={<CircleAlert className="h-4 w-4 text-destructive" aria-hidden />}
        label="Server unreachable"
      />
      <Rows>
        <Row label="Code" value={<code className="font-mono">{code}</code>} />
        <Row label="Message" value={message} />
      </Rows>
    </CardShell>
  );
}

function Ok({ data }: { data: HealthResponse }) {
  return (
    <CardShell>
      <Header
        icon={<CircleCheck className="h-4 w-4 text-success" aria-hidden />}
        label={`Server: ${data.status}`}
      />
      <Rows>
        <Row label="Account" value={<code className="font-mono">{data.account}</code>} />
        <Row label="Version" value={data.version} />
        <Row label="Started" value={formatStarted(data.startedAt)} />
      </Rows>
    </CardShell>
  );
}

function Header({ icon, label }: { icon: React.ReactNode; label: React.ReactNode }) {
  return (
    <div id="health-heading" className="mb-4 flex items-center gap-2 text-sm font-medium">
      {icon}
      <span>{label}</span>
    </div>
  );
}

function Rows({ children }: { children: React.ReactNode }) {
  return <dl className="grid grid-cols-[5rem_1fr] gap-y-1 text-sm">{children}</dl>;
}

function Row({ label, value }: { label: React.ReactNode; value: React.ReactNode }) {
  return (
    <>
      <dt className="text-foreground/60">{label}</dt>
      <dd className="text-foreground">{value}</dd>
    </>
  );
}

function Skeleton({ className }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={cn('inline-block h-4 animate-pulse rounded bg-foreground/10', className)}
    />
  );
}

function formatStarted(iso: string): string {
  try {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return iso;
    return d.toLocaleString(undefined, {
      year: 'numeric',
      month: 'short',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    });
  } catch {
    return iso;
  }
}
