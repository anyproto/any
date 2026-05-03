import { useAtom } from 'jotai';
import { Sun, Moon, Monitor } from 'lucide-react';
import { themePreferenceAtom, type ThemePreference } from '@/atoms';
import { HealthCard } from '@/components/health';
import { useHealth } from '@/lib/api/meta';

/**
 * Health page — the only screen in PR #1.
 *
 * Renders a three-state card for /v1/health. Replaced by the 3-pane
 * layout shell in PR #2.
 */
export function HealthPage() {
  const health = useHealth();
  return (
    <main className="mx-auto flex min-h-screen max-w-xl flex-col gap-6 p-8">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold tracking-tight">any</h1>
        <ThemeToggle />
      </header>
      <HealthCard query={health} />
    </main>
  );
}

const OPTIONS: readonly {
  value: ThemePreference;
  label: string;
  icon: typeof Sun;
}[] = [
  { value: 'light', label: 'White', icon: Sun },
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
