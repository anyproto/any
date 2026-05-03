import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useState,
  type ComponentType,
  type LazyExoticComponent,
  type SVGProps,
} from 'react';
import dynamicIconImports from 'lucide-react/dynamicIconImports.js';
import { Sparkles } from 'lucide-react';
import { Button, Input } from '@/components/ui';
import { cn } from '@/lib/cn';
import { listIconToken, lucideIconIdFromToken } from './listIconTokens';

type LucideIconId = keyof typeof dynamicIconImports;
type DynamicIconComponent = ComponentType<SVGProps<SVGSVGElement>>;

const DEFAULT_ICON_LIMIT = 144;
const SEARCH_ICON_LIMIT = 240;
const ICON_PAGE_SIZE = 144;

const POPULAR_LUCIDE_ICON_IDS = [
  'sparkles',
  'book-open',
  'notebook',
  'file-text',
  'newspaper',
  'bookmark',
  'folder-open',
  'archive',
  'box',
  'package',
  'film',
  'clapperboard',
  'music',
  'gamepad-2',
  'palette',
  'brush',
  'pen-tool',
  'camera',
  'wand-sparkles',
  'lightbulb',
  'star',
  'heart',
  'check-circle-2',
  'trophy',
  'crown',
  'graduation-cap',
  'briefcase',
  'wallet',
  'shopping-bag',
  'coffee',
  'utensils',
  'dumbbell',
  'bike',
  'car',
  'plane',
  'map',
  'map-pin',
  'compass',
  'flag',
  'globe',
  'home',
  'calendar-days',
  'clock',
  'umbrella',
  'sun',
  'moon',
  'zap',
  'smile',
  'users',
  'shield',
  'lock',
  'key-round',
  'mail',
  'phone',
  'link',
  'code',
  'terminal',
  'database',
  'server',
  'cpu',
  'laptop',
  'rocket',
  'tags',
  'puzzle',
  'shapes',
  'gift',
] as const;

const ICON_ALIASES: Record<string, string> = {
  'book-open': 'book read pages library',
  clapperboard: 'movie film cinema video',
  film: 'movie cinema video',
  'gamepad-2': 'game gaming play console',
  'graduation-cap': 'school study learn education',
  lightbulb: 'idea thought',
  'shopping-bag': 'shop store buy',
  tags: 'tag label categories',
  'wand-sparkles': 'magic ai',
};

const dynamicIconCache = new Map<string, LazyExoticComponent<DynamicIconComponent>>();
const ALL_LIST_ICON_IDS = buildIconCatalog();
const TOTAL_ICON_COUNT = Object.keys(dynamicIconImports).length;

export function LucideIconRenderer({
  id,
  className,
}: {
  id: string;
  className?: string | undefined;
}) {
  const LazyIcon = lazyLucideIcon(id);
  if (!LazyIcon) return <Sparkles className={className} aria-hidden />;
  return (
    <Suspense
      fallback={<span aria-hidden className={cn('inline-block h-5 w-5 shrink-0', className)} />}
    >
      <LazyIcon className={className} aria-hidden />
    </Suspense>
  );
}

export function ListLucideIconCatalogPane({
  icon,
  onChoose,
}: {
  icon?: string | undefined;
  onChoose: (icon: string) => void;
}) {
  const [query, setQuery] = useState('');
  const [visibleIconLimit, setVisibleIconLimit] = useState(DEFAULT_ICON_LIMIT);
  const normalizedQuery = query.trim().toLowerCase();
  const activeLucideIconId = activeIconId(icon);

  useEffect(() => {
    setVisibleIconLimit(normalizedQuery ? SEARCH_ICON_LIMIT : DEFAULT_ICON_LIMIT);
  }, [normalizedQuery]);

  const orderedIconIds = useMemo(() => {
    if (!activeLucideIconId) return ALL_LIST_ICON_IDS;
    return [
      activeLucideIconId,
      ...ALL_LIST_ICON_IDS.filter((iconId) => iconId !== activeLucideIconId),
    ];
  }, [activeLucideIconId]);

  const matchedIconIds = useMemo(() => {
    if (!normalizedQuery) return orderedIconIds;
    return orderedIconIds.filter((iconId) => iconSearchText(iconId).includes(normalizedQuery));
  }, [normalizedQuery, orderedIconIds]);

  const visibleIconIds = matchedIconIds.slice(0, visibleIconLimit);
  const hasMoreIcons = matchedIconIds.length > visibleIconIds.length;

  return (
    <>
      <div className="border-b border-foreground/8 p-4">
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-9 border-0 bg-foreground/[0.055] shadow-none"
          placeholder="Search icons..."
          aria-label="Search icons"
        />
        <div className="mt-2 flex items-center justify-between px-1 text-[11px] font-medium text-foreground/35">
          <span>{TOTAL_ICON_COUNT.toLocaleString()} icons</span>
          <span>
            {matchedIconIds.length.toLocaleString()} match
            {matchedIconIds.length === 1 ? '' : 'es'}
          </span>
        </div>
      </div>
      <div className="max-h-[340px] overflow-y-auto p-4">
        {visibleIconIds.length === 0 ? (
          <p className="px-1 py-6 text-center text-sm text-foreground/45">No icons found.</p>
        ) : (
          <>
            <div className="grid grid-cols-10 gap-1.5 sm:grid-cols-12">
              {visibleIconIds.map((iconId) => {
                const token = listIconToken(iconId);
                const label = iconLabel(iconId);
                return (
                  <button
                    key={iconId}
                    type="button"
                    aria-label={`Use ${label} icon`}
                    title={label}
                    onClick={() => onChoose(token)}
                    className={cn(
                      'flex h-9 w-9 items-center justify-center rounded-md text-foreground/65',
                      'hover:bg-foreground/7 hover:text-foreground',
                      'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                      icon === token && 'bg-accent/15 text-accent ring-1 ring-accent/40',
                    )}
                  >
                    <LucideIconRenderer id={iconId} className="h-5 w-5" />
                  </button>
                );
              })}
            </div>
            {hasMoreIcons && (
              <Button
                type="button"
                variant="ghost"
                className="mt-3 h-9 w-full"
                onClick={() =>
                  setVisibleIconLimit((limit) =>
                    Math.min(limit + ICON_PAGE_SIZE, matchedIconIds.length),
                  )
                }
              >
                More icons
              </Button>
            )}
          </>
        )}
      </div>
    </>
  );
}

function activeIconId(icon: string | undefined) {
  if (!icon) return undefined;
  const id = lucideIconIdFromToken(icon);
  return id && isLucideIconId(id) ? id : undefined;
}

function isLucideIconId(id: string): id is LucideIconId {
  return Object.prototype.hasOwnProperty.call(dynamicIconImports, id);
}

function buildIconCatalog() {
  const popularIds = POPULAR_LUCIDE_ICON_IDS.filter(isLucideIconId);
  const seen = new Set<string>(popularIds);
  const remainingIds = Object.keys(dynamicIconImports)
    .filter((id) => !seen.has(id))
    .sort((a, b) => iconLabel(a).localeCompare(iconLabel(b)));
  return [...popularIds, ...remainingIds];
}

function iconLabel(id: string) {
  return id
    .split('-')
    .map((part) =>
      part.length === 0
        ? ''
        : part.length === 1
          ? part.toUpperCase()
          : `${part[0]?.toUpperCase()}${part.slice(1)}`,
    )
    .join(' ');
}

function iconSearchText(id: string) {
  return `${id} ${iconLabel(id)} ${ICON_ALIASES[id] ?? ''}`.toLowerCase();
}

function lazyLucideIcon(id: string) {
  if (!isLucideIconId(id)) return undefined;
  const cachedIcon = dynamicIconCache.get(id);
  if (cachedIcon) return cachedIcon;

  const loader = dynamicIconImports[id];
  const LazyIcon = lazy(async () => {
    const mod = await loader();
    return { default: mod.default as DynamicIconComponent };
  });
  dynamicIconCache.set(id, LazyIcon);
  return LazyIcon;
}
