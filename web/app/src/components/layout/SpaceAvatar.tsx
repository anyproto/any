import { tone, glyph, type SpaceTone } from '@/lib/space-visual';
import { cn } from '@/lib/cn';

const TONE_BG: Record<SpaceTone, string> = {
  accent: 'bg-accent text-background',
  success: 'bg-success text-background',
  info: 'bg-info text-background',
  destructive: 'bg-destructive text-background',
  foreground: 'bg-foreground/80 text-background',
};

interface SpaceAvatarProps {
  spaceId: string;
  /** Display name; falls back to id-derived glyph if absent. */
  name?: string | undefined;
  /** Pixel size shorthand. Maps onto a fixed Tailwind h/w pair. */
  size?: 'xs' | 'sm' | 'md';
  /** Active state — adds an accent ring with offset. */
  active?: boolean;
  /** Muted state — fades the avatar (used for non-active space statuses). */
  muted?: boolean;
  /**
   * User-chosen emoji or short text shown in place of the auto
   * initial-glyph. The tone (background color) still derives from
   * spaceId so a renamed space keeps its visual identity.
   */
  iconOverride?: string | undefined;
  /** Optional extra classes for fine-tuning at the call site. */
  className?: string;
}

const SIZE_CLASSES: Record<NonNullable<SpaceAvatarProps['size']>, string> = {
  xs: 'h-4 w-4 text-[9px] rounded',
  sm: 'h-5 w-5 text-[11px] rounded',
  md: 'h-9 w-9 text-sm rounded-lg',
};

/**
 * Single-glyph rounded-square avatar for a space. Used in pane 1's
 * vertical rail (md) and pane 2's header (sm). The shape (rounded
 * square, not circle) distinguishes spaces from people/accounts.
 */
export function SpaceAvatar({
  spaceId,
  name,
  size = 'md',
  active,
  muted,
  iconOverride,
  className,
}: SpaceAvatarProps) {
  const t = tone(spaceId);
  const trimmedIcon = iconOverride?.trim() || undefined;
  return (
    <span
      aria-hidden
      className={cn(
        'inline-flex items-center justify-center font-semibold leading-none transition-all',
        SIZE_CLASSES[size],
        TONE_BG[t],
        active && 'ring-2 ring-accent ring-offset-2 ring-offset-background',
        muted && 'opacity-50',
        className,
      )}
    >
      {trimmedIcon ?? glyph(name, spaceId)}
    </span>
  );
}
