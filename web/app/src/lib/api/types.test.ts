import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  listTypes,
  createType,
  addPropertyToType,
  getTypeProperties,
  uiKind,
  toAddPropertyParts,
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
});
