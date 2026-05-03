import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { axe } from 'vitest-axe';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SpacesRail } from './SpacesRail';
import { activeSpaceIdAtom, activeViewAtom, settingsOpenAtom } from '@/atoms';
import type { SpaceInfo } from '@/lib/api/spaces';

const SAMPLE_SPACES: SpaceInfo[] = [
  {
    id: 'spc-real-1',
    name: 'Personal',
    status: 'active',
    ownRole: 'owner',
    createdAt: '2026-04-01T00:00:00Z',
  },
  {
    id: 'spc-real-2',
    name: 'Work',
    status: 'active',
    ownRole: 'owner',
    createdAt: '2026-04-02T00:00:00Z',
  },
];

function fakeHealth(): Response {
  return new Response(
    JSON.stringify({
      status: 'ok',
      version: 'test',
      startedAt: '2026-05-02T00:00:00Z',
      account: 'A8AhvwZmrRHbRPWswsU2MZgn7JYKCd4z145kmeV2PMTscGMA',
    }),
    { status: 200, headers: { 'Content-Type': 'application/json' } },
  );
}

function fakeSpaces(spaces: SpaceInfo[]): Response {
  return new Response(JSON.stringify({ spaces }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function requestUrl(input: Parameters<typeof fetch>[0]): string {
  return typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
}

function renderRail({
  spaces,
  initialActiveId = null,
  failWith,
  configureStore,
}: {
  spaces?: SpaceInfo[];
  initialActiveId?: string | null;
  failWith?: { status: number; body: string };
  configureStore?: (store: ReturnType<typeof createStore>) => void;
} = {}) {
  // Per-call mock so each request gets a fresh Response (Body is single-use).
  // Routes by URL substring so the AccountAvatar's /v1/health call also works.
  vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
    const url = requestUrl(input);
    if (url.includes('/v1/health')) {
      return Promise.resolve(fakeHealth());
    }
    if (/\/v1\/spaces\/[^/]+\/objects\/[^/]+\/markdown$/.test(url)) {
      return Promise.resolve(
        new Response(JSON.stringify({ content: '# Preview' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
    if (/\/v1\/spaces\/[^/]+\/types$/.test(url)) {
      return Promise.resolve(
        new Response(JSON.stringify({ types: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
    if (url.endsWith('/objects/query')) {
      return Promise.resolve(
        new Response(JSON.stringify({ records: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
    const spaceDetail = (spaces ?? []).find((space) => url.endsWith(`/v1/spaces/${space.id}`));
    if (spaceDetail) {
      return Promise.resolve(
        new Response(JSON.stringify(spaceDetail), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      );
    }
    if (failWith) {
      return Promise.resolve(new Response(failWith.body, { status: failWith.status }));
    }
    return Promise.resolve(fakeSpaces(spaces ?? []));
  });

  const store = createStore();
  store.set(activeSpaceIdAtom, initialActiveId);
  configureStore?.(store);
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <SpacesRail />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, store, qc };
}

describe('<SpacesRail>', () => {
  it('renders the new-space + button regardless of state', () => {
    renderRail({ spaces: [] });
    expect(screen.getByRole('button', { name: 'New space' })).toBeInTheDocument();
  });

  it('opens settings from the bottom cog', async () => {
    const { store } = renderRail({ spaces: [] });
    await userEvent.click(screen.getByRole('button', { name: 'Settings' }));
    expect(store.get(settingsOpenAtom)).toBe(true);
  });

  it('keeps filtering and space buttons available in the open panel', async () => {
    renderRail({ spaces: SAMPLE_SPACES });
    expect(screen.getByRole('textbox', { name: 'Filter spaces' })).toBeInTheDocument();
    await screen.findByRole('button', { name: SAMPLE_SPACES[0]!.name! });
  });

  it('renders one icon per space when /v1/spaces resolves', async () => {
    renderRail({ spaces: SAMPLE_SPACES });
    for (const s of SAMPLE_SPACES) {
      await screen.findByRole('button', { name: s.name! });
    }
  });

  it('clicking a space updates the active-space atom', async () => {
    const { store } = renderRail({ spaces: SAMPLE_SPACES });
    const target = SAMPLE_SPACES[1]!;
    const btn = await screen.findByRole('button', { name: target.name! });
    await userEvent.click(btn);
    expect(store.get(activeSpaceIdAtom)).toBe(target.id);
  });

  it('preloads the restored object view when previewing a space', async () => {
    renderRail({
      spaces: SAMPLE_SPACES,
      configureStore: (store) => {
        store.set(activeSpaceIdAtom, SAMPLE_SPACES[1]!.id);
        store.set(activeViewAtom, { kind: 'object', objectId: 'obj-preview' });
        store.set(activeSpaceIdAtom, SAMPLE_SPACES[0]!.id);
      },
    });
    const target = SAMPLE_SPACES[1]!;
    const btn = await screen.findByRole('button', { name: target.name! });
    await userEvent.hover(btn);

    await waitFor(() => {
      const calls = vi.mocked(globalThis.fetch).mock.calls;
      expect(
        calls.some(([input]) => requestUrl(input).includes('/objects/obj-preview/markdown')),
      ).toBe(true);
    });
  });

  it('marks the active space with aria-current', async () => {
    const target = SAMPLE_SPACES[0]!;
    renderRail({ spaces: SAMPLE_SPACES, initialActiveId: target.id });
    const btn = await screen.findByRole('button', { name: target.name! });
    expect(btn).toHaveAttribute('aria-current', 'page');
  });

  it('shows the error treatment on a failed list', async () => {
    renderRail({
      failWith: {
        status: 500,
        body: JSON.stringify({
          error: { code: 'server.internal', message: 'boom' },
        }),
      },
    });
    await waitFor(() => {
      expect(screen.getByRole('img', { name: /spaces unavailable/i })).toBeInTheDocument();
    });
  });

  it('has no axe violations (populated)', async () => {
    const { container } = renderRail({ spaces: SAMPLE_SPACES });
    await screen.findByRole('button', { name: SAMPLE_SPACES[0]!.name! });
    expect(await axe(container)).toHaveNoViolations();
  });
});
