/**
 * Mock data for the layout shell.
 *
 * The MOCK_SPACES list was removed in PR #3 (rail now uses
 * /v1/spaces). Sections + objects below are still mock; PR #4 wires
 * /v1/spaces/:s/objects/query and deletes the rest of this file.
 */

export interface MockSection {
  id: string;
  label: string;
  collapsible?: boolean;
  defaultCollapsed?: boolean;
  items: MockItem[];
}

export interface MockItem {
  id: string;
  title: string;
  glyph?: string;
  badge?: { kind: 'unread' | 'mention'; count: number };
}

export interface MockObject {
  id: string;
  title: string;
  body: string[];
  breadcrumb: string[];
}

const ANYTYPE_TEAM_SECTIONS: MockSection[] = [
  {
    id: 'sec-channels',
    label: 'Channels',
    items: [
      { id: 'obj-chats', title: 'Chats', glyph: '💬' },
      { id: 'obj-random', title: 'Random', glyph: '🎲' },
      { id: 'obj-security', title: 'Security', glyph: '🔒' },
      { id: 'obj-ai', title: 'AI', glyph: '🤖' },
      { id: 'obj-bugs', title: 'Bugs (desktop)', glyph: '🐛' },
      { id: 'obj-app-ios', title: 'App iOS', glyph: '🍎' },
      { id: 'obj-release', title: 'release-hustle', glyph: '🚢' },
    ],
  },
  {
    id: 'sec-unread',
    label: 'Unread',
    collapsible: true,
    items: [
      { id: 'obj-bugs', title: 'Bugs (desktop)', glyph: '🐛', badge: { kind: 'unread', count: 5 } },
      { id: 'obj-app-ios', title: 'App iOS', glyph: '🍎', badge: { kind: 'mention', count: 23 } },
    ],
  },
  {
    id: 'sec-favorites',
    label: 'My Favorites',
    collapsible: true,
    items: [
      { id: 'obj-product-design', title: 'Product & Design', glyph: '🌿' },
      { id: 'obj-agents', title: 'Agents', glyph: '🛟' },
      { id: 'obj-app-desktop', title: 'App Desktop', glyph: '🖥️' },
    ],
  },
  {
    id: 'sec-types',
    label: 'Types',
    collapsible: true,
    items: [
      { id: 'obj-use-cases', title: 'Use Cases', glyph: '📁' },
      { id: 'obj-tools', title: 'Tools', glyph: '🔨' },
      { id: 'obj-wikis', title: 'Wikis', glyph: '📖' },
      { id: 'obj-feature-requests', title: 'Feature Requests', glyph: '💡' },
      { id: 'obj-issues', title: 'Issues', glyph: '🎯' },
    ],
  },
];

const PERSONAL_SECTIONS: MockSection[] = [
  {
    id: 'sec-recent',
    label: 'Recent',
    items: [
      { id: 'obj-journal', title: 'Daily Journal', glyph: '📓' },
      { id: 'obj-reading', title: 'Reading List', glyph: '📚' },
      { id: 'obj-recipes', title: 'Recipes', glyph: '🍳' },
    ],
  },
  {
    id: 'sec-types',
    label: 'Types',
    collapsible: true,
    items: [
      { id: 'obj-notes', title: 'Notes', glyph: '📝' },
      { id: 'obj-tasks', title: 'Tasks', glyph: '✅' },
    ],
  },
];

export function mockSectionsForSpace(spaceId: string): MockSection[] {
  if (spaceId === 'spc-anytype-team') return ANYTYPE_TEAM_SECTIONS;
  if (spaceId === 'spc-personal') return PERSONAL_SECTIONS;
  return [
    {
      id: 'sec-empty',
      label: 'Channels',
      items: [{ id: 'obj-welcome', title: 'Welcome', glyph: '👋' }],
    },
  ];
}

const SAMPLE_BODY = [
  "This is a placeholder body for the layout shell — PR #2 doesn't wire the editor yet.",
  "When PR #5 lands, this view becomes a BlockNote-powered markdown editor that round-trips through GET/PUT /v1/spaces/:s/objects/:o/markdown.",
  'Until then, the body just renders a few paragraphs so the type ramp, line height, and surface tokens can be checked in both light and dark mode.',
];

export function mockObjectById(id: string | null): MockObject | null {
  if (!id) return null;
  // Find the title across all sections; fall back to the id if not in mock.
  const allSections = [...ANYTYPE_TEAM_SECTIONS, ...PERSONAL_SECTIONS];
  for (const s of allSections) {
    for (const item of s.items) {
      if (item.id === id) {
        return {
          id,
          title: item.title,
          body: SAMPLE_BODY,
          breadcrumb: ['Anytype Team', s.label, item.title],
        };
      }
    }
  }
  return { id, title: id, body: SAMPLE_BODY, breadcrumb: [id] };
}
