export const LUCIDE_ICON_PREFIX = 'lucide:';

export function listIconToken(id: string) {
  return `${LUCIDE_ICON_PREFIX}${id}`;
}

export function lucideIconIdFromToken(icon: string): string | undefined {
  if (!icon.startsWith(LUCIDE_ICON_PREFIX)) return undefined;
  const id = icon.slice(LUCIDE_ICON_PREFIX.length).trim();
  return id || undefined;
}
