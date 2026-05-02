import { describe, it, expect, beforeEach, vi } from 'vitest';
import { queryObjects, createObject } from './objects';
import { ApiError } from './client';

describe('objects API', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('queryObjects POSTs the right URL with the body as JSON', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ records: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await queryObjects('spc-abc', { filter: { 'nav.parentId': '' }, sort: ['nav.pos'] });
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-abc/objects/query');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe(
      JSON.stringify({ filter: { 'nav.parentId': '' }, sort: ['nav.pos'] }),
    );
  });

  it('queryObjects returns the records array (empty if absent)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const out = await queryObjects('spc-abc', {});
    expect(out).toEqual([]);
  });

  it('createObject POSTs an empty body when not provided', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ objectId: 'obj-1' }), {
        status: 201,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const out = await createObject('spc-abc');
    expect(out).toEqual({ objectId: 'obj-1' });
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-abc/objects');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe('{}');
  });

  it('queryObjects surfaces error envelope as ApiError', async () => {
    // Each call needs a fresh Response — Body is single-use.
    vi.spyOn(globalThis, 'fetch').mockImplementation(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            error: { code: 'space.not_found', message: 'no space' },
          }),
          { status: 404, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    );
    await expect(queryObjects('spc-bad', {})).rejects.toMatchObject({
      name: 'ApiError',
      code: 'space.not_found',
      status: 404,
    });
    void ApiError; // keep ApiError import live (used as a type marker)
  });
});
