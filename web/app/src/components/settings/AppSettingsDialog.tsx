import { useAtom } from 'jotai';
import { Monitor, Moon, Sun, type LucideIcon } from 'lucide-react';
import { settingsOpenAtom, themePreferenceAtom, type ThemePreference } from '@/atoms';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui';
import { cn } from '@/lib/cn';

const THEME_OPTIONS: readonly {
  value: ThemePreference;
  label: string;
  description: string;
  icon: LucideIcon;
}[] = [
  {
    value: 'system',
    label: 'System',
    description: 'Follow this device',
    icon: Monitor,
  },
  {
    value: 'light',
    label: 'White',
    description: 'Always use the white interface',
    icon: Sun,
  },
  {
    value: 'dark',
    label: 'Dark',
    description: 'Always use the dark interface',
    icon: Moon,
  },
];

export function AppSettingsDialog() {
  const [open, setOpen] = useAtom(settingsOpenAtom);
  const [themePreference, setThemePreference] = useAtom(themePreferenceAtom);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Settings</DialogTitle>
          <DialogDescription>
            Configure how the app looks on this device.
          </DialogDescription>
        </DialogHeader>

        <section className="mt-5">
          <div className="mb-3">
            <h2 className="text-sm font-medium text-foreground">Appearance</h2>
            <p className="mt-1 text-xs leading-5 text-foreground/55">
              Theme preference is stored locally and applied immediately.
            </p>
          </div>

          <div
            role="radiogroup"
            aria-label="Theme"
            className="grid grid-cols-3 gap-2"
          >
            {THEME_OPTIONS.map(({ value, label, description, icon: Icon }) => {
              const active = themePreference === value;
              return (
                <button
                  key={value}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  onClick={() => setThemePreference(value)}
                  className={cn(
                    'group flex min-h-[92px] flex-col items-start justify-between rounded-lg border p-3 text-left',
                    'transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                    active
                      ? 'border-accent bg-accent/10 text-foreground'
                      : 'border-foreground/10 bg-foreground/[0.02] text-foreground/75 hover:border-foreground/20 hover:bg-foreground/[0.04] hover:text-foreground',
                  )}
                >
                  <span
                    className={cn(
                      'inline-flex h-7 w-7 items-center justify-center rounded-md',
                      active ? 'bg-accent text-background' : 'bg-foreground/8 text-foreground/60',
                    )}
                    aria-hidden
                  >
                    <Icon className="h-4 w-4" />
                  </span>
                  <span className="min-w-0">
                    <span className="block text-sm font-medium">{label}</span>
                    <span className="mt-0.5 block text-xs leading-4 text-foreground/55">
                      {description}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        </section>
      </DialogContent>
    </Dialog>
  );
}
