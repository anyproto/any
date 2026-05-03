import { tone, type SpaceTone } from '@/lib/space-visual';
import { useHealth } from '@/lib/api/meta';
import { SimpleTooltip } from '@/components/ui';
import { cn } from '@/lib/cn';

const TONE_BG: Record<SpaceTone, string> = {
  accent: 'bg-accent text-background',
  success: 'bg-success text-background',
  info: 'bg-info text-background',
  destructive: 'bg-destructive text-background',
  foreground: 'bg-foreground/80 text-background',
};

/**
 * Account avatar pinned at the bottom of pane 1.
 *
 * Circle (not rounded square) — visually distinguishes "you" from
 * spaces. Token-derived from the account id (no real avatar API yet).
 * Click is a no-op until a settings/account page lands.
 */
export function AccountAvatar() {
  const health = useHealth();
  const account = health.data?.account ?? '';
  const initial = account.charAt(0).toUpperCase() || '?';
  const t = tone(account || 'unknown');

  return (
    <SimpleTooltip text={account ? account.slice(0, 12) + '…' : 'Account'} side="right">
      <button
        type="button"
        aria-label="Account"
        disabled
        className={cn(
          'inline-flex h-7 w-7 items-center justify-center rounded-full text-[11px] font-semibold leading-none',
          TONE_BG[t],
          'opacity-90 disabled:cursor-not-allowed',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-background',
        )}
      >
        {initial}
      </button>
    </SimpleTooltip>
  );
}
