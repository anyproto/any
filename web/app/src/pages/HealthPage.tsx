import { useAtom } from 'jotai';
import { Sun, Moon, Monitor } from 'lucide-react';
import { themePreferenceAtom, type ThemePreference } from '@/atoms/theme';

/**
 * Health page — the only screen in PR #1.
 *
 * Stage 1 stub: just enough surface to verify the dev loop and embed
 * pipeline are wired correctly. Stage 3 fills in the real
 * `/v1/health` query + three-state `HealthCard`.
 */
export function HealthPage() {
  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col gap-6 p-8">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">any</h1>
        <ThemeToggle />
      </header>

      <section
        aria-labelledby="health-heading"
        className="rounded-lg border border-foreground/10 bg-background p-6 shadow-sm"
      >
        <h2 id="health-heading" className="mb-2 text-base font-medium">
          Server health
        </h2>
        <p className="text-foreground/70">
          Wiring under construction — the live <code>/v1/health</code> query
          lands in stage 3.
        </p>
      </section>
    </main>
  );
}

const OPTIONS: ReadonlyArray<{
  value: ThemePreference;
  label: string;
  icon: typeof Sun;
}> = [
  { value: 'light', label: 'Light', icon: Sun },
  { value: 'system', label: 'System', icon: Monitor },
  { value: 'dark', label: 'Dark', icon: Moon },
];

function ThemeToggle() {
  const [pref, setPref] = useAtom(themePreferenceAtom);
  return (
    <div
      role="radiogroup"
      aria-label="Theme"
      className="inline-flex gap-1 rounded-md border border-foreground/10 p-1"
    >
      {OPTIONS.map(({ value, label, icon: Icon }) => {
        const active = pref === value;
        return (
          <button
            key={value}
            type="button"
            role="radio"
            aria-checked={active}
            aria-label={label}
            title={label}
            onClick={() => setPref(value)}
            className={
              'inline-flex h-7 w-7 items-center justify-center rounded transition ' +
              (active
                ? 'bg-foreground/10 text-foreground'
                : 'text-foreground/60 hover:bg-foreground/5 hover:text-foreground')
            }
          >
            <Icon className="h-4 w-4" aria-hidden />
          </button>
        );
      })}
    </div>
  );
}
