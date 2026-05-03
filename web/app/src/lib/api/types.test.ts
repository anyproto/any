import { describe, it, expect, beforeEach, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { createElement, type ReactNode } from 'react';
import {
  listTypes,
  createType,
  addPropertyToType,
  getTypeProperties,
  useType,
  findPagesListId,
  visibleUserTypes,
  uiKind,
  toAddPropertyParts,
  type TypeInfo,
  type UIPropertyKind,
} from './types';

describe('types API', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('listTypes hits the right URL and returns types', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ types: [{ id: 't1', name: 'Recipe' }] }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const out = await listTypes('spc-a');
    expect(out).toEqual([{ id: 't1', name: 'Recipe' }]);
    const [url] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-a/types');
  });

  it('createType POSTs name + description', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ typeId: 't_new' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const out = await createType('spc-a', { name: 'Recipe', description: 'A dish' });
    expect(out).toEqual({ typeId: 't_new' });
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-a/types');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe(JSON.stringify({ name: 'Recipe', description: 'A dish' }));
  });

  it('addPropertyToType POSTs to .../properties', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ propId: 'p_servings' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const out = await addPropertyToType('spc-a', 't_recipe', {
      name: 'Servings',
      kind: 'number',
    });
    expect(out).toEqual({ propId: 'p_servings' });
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-a/types/t_recipe/properties');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe(JSON.stringify({ name: 'Servings', kind: 'number' }));
  });

  it('uiKind maps (kind, xKey) → UI kind', () => {
    expect(uiKind({ kind: 'string' })).toBe('string');
    expect(uiKind({ kind: 'string', xKey: 'longtext' })).toBe('longtext');
    expect(uiKind({ kind: 'string', xKey: 'date' })).toBe('date');
    expect(uiKind({ kind: 'string', xKey: 'url' })).toBe('url');
    expect(uiKind({ kind: 'string', xKey: 'email' })).toBe('email');
    expect(uiKind({ kind: 'array', xKey: 'tags' })).toBe('tags');
    expect(uiKind({ kind: 'string', xKey: 'relation' })).toBe('relation');
    expect(uiKind({ kind: 'array' })).toBe('array');
    expect(uiKind({ kind: 'number' })).toBe('number');
    expect(uiKind({ kind: 'boolean' })).toBe('boolean');
    // Unknown xKey on string still falls back to string.
    expect(uiKind({ kind: 'string', xKey: 'someother' })).toBe('string');
  });

  it('toAddPropertyParts inverts uiKind for the AddProperty body', () => {
    const cases: [UIPropertyKind, { kind: string; xKey?: string }][] = [
      ['string', { kind: 'string' }],
      ['longtext', { kind: 'string', xKey: 'longtext' }],
      ['number', { kind: 'number' }],
      ['boolean', { kind: 'boolean' }],
      ['date', { kind: 'string', xKey: 'date' }],
      ['url', { kind: 'string', xKey: 'url' }],
      ['email', { kind: 'string', xKey: 'email' }],
      ['tags', { kind: 'array', xKey: 'tags' }],
      ['relation', { kind: 'string', xKey: 'relation' }],
    ];
    for (const [ui, expected] of cases) {
      expect(toAddPropertyParts(ui)).toEqual(expected);
    }
  });

  it('getTypeProperties returns the array', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          properties: [
            { id: 'p1', name: 'Servings', kind: 'number' },
            { id: 'p2', name: 'Vegan', kind: 'boolean' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const out = await getTypeProperties('spc-a', 't_recipe');
    expect(out).toHaveLength(2);
    expect(out[0]!.name).toBe('Servings');
  });

  it('useType seeds detail data from the cached type list', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    });
    const cachedTypes: TypeInfo[] = [{ id: 't_movie', name: 'Movies' }];
    qc.setQueryData(['types', 'spc-a'], cachedTypes);

    const { result } = renderHook(() => useType('spc-a', 't_movie'), {
      wrapper: ({ children }: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client: qc }, children),
    });

    expect(result.current.data).toEqual({ id: 't_movie', name: 'Movies' });
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it('useType keeps cached list metadata when the detail response is sparse', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ id: 't_movie' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    qc.setQueryData<TypeInfo[]>(['types', 'spc-a'], [{ id: 't_movie', name: 'Movies' }]);

    const { result } = renderHook(() => useType('spc-a', 't_movie'), {
      wrapper: ({ children }: { children: ReactNode }) =>
        createElement(QueryClientProvider, { client: qc }, children),
    });

    await waitFor(() => expect(fetchSpy).toHaveBeenCalled());
    await waitFor(() => {
      expect(result.current.data).toEqual({ id: 't_movie', name: 'Movies' });
    });
  });

  it('findPagesListId returns the first non-built-in Pages list', () => {
    expect(
      findPagesListId([
        { id: 'builtin-pages', name: 'Pages', builtIn: true },
        { id: 't-pages-1', name: 'Pages' },
        { id: 't-pages-2', name: 'Pages' },
      ]),
    ).toBe('t-pages-1');
  });

  it('visibleUserTypes hides built-ins and collapses duplicate Pages lists', () => {
    expect(
      visibleUserTypes([
        { id: 'nav', name: 'nav', builtIn: true },
        { id: 't-pages-1', name: 'Pages' },
        { id: 't-books', name: 'Books' },
        { id: 't-pages-2', name: 'Pages' },
        { id: 't-movies', name: 'Movies' },
      ]),
    ).toEqual([
      { id: 't-pages-1', name: 'Pages' },
      { id: 't-books', name: 'Books' },
      { id: 't-movies', name: 'Movies' },
    ]);
  });
});
