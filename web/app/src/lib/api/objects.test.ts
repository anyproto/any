import { describe, it, expect, beforeEach, vi } from 'vitest';
import { QueryClient } from '@tanstack/react-query';
import {
  queryObjects,
  createObject,
  setObjectProperty,
  deleteObject,
  buildCreateObjectBody,
  findObjectNameInCache,
  objectKeys,
  patchObjectProp,
  readObjectProp,
  type ObjectRecord,
} from './objects';
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

  it('queryObjects returns the records array', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ records: [{ id: 'obj-1' }] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const out = await queryObjects('spc-abc', {});
    expect(out).toEqual([{ id: 'obj-1' }]);
  });

  it('queryObjects rejects invalid response shapes', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await expect(queryObjects('spc-abc', {})).rejects.toMatchObject({
      code: 'client.bad_response',
      status: 200,
    });
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

  it('buildCreateObjectBody makes the hierarchy home explicit', () => {
    expect(buildCreateObjectBody()).toEqual({
      nav: { type: 1, parentId: '' },
    });
    expect(buildCreateObjectBody({ parentId: 'folder-1' })).toEqual({
      nav: { type: 1, parentId: 'folder-1' },
    });
  });

  it('buildCreateObjectBody keeps list membership separate from hierarchy', () => {
    expect(buildCreateObjectBody({ typeIds: ['t_movie'] })).toEqual({
      nav: { type: 1, parentId: '' },
      types: ['t_movie'],
    });
    expect(buildCreateObjectBody({ folder: true, pos: 'PPQY' })).toEqual({
      nav: { type: 2, parentId: '', pos: 'PPQY' },
    });
  });

  it('reads and patches object namespaces through the model facade', () => {
    const row: ObjectRecord = {
      id: 'obj-1',
      any: { name: 'Alien' },
      t_movie: { p_rating: 5 },
    };

    const next = patchObjectProp(row, 't_movie', 'p_rating', 6);

    expect(readObjectProp(row, 't_movie', 'p_rating')).toBe(5);
    expect(readObjectProp(next, 't_movie', 'p_rating')).toBe(6);
    expect(row.t_movie).toEqual({ p_rating: 5 });
  });

  it('finds object names in existing object caches for cold editor opens', () => {
    const qc = new QueryClient();
    qc.setQueryData<ObjectRecord[]>(objectKeys.childrenOf('spc-abc', ''), [
      { id: 'obj-1', any: { name: 'Cached page' } },
    ]);

    expect(findObjectNameInCache(qc, 'spc-abc', 'obj-1')).toBe('Cached page');
  });

  it('setObjectProperty POSTs {patch} to .../base/:t', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    await setObjectProperty('spc-abc', 'obj-1', 'any', { name: 'New' });
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-abc/properties/obj-1/base/any');
    expect(init?.method).toBe('POST');
    expect(init?.body).toBe(JSON.stringify({ patch: { name: 'New' } }));
  });

  it('deleteObject issues DELETE with no body', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 }),
    );
    await deleteObject('spc-abc', 'obj-1');
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-abc/objects/obj-1');
    expect(init?.method).toBe('DELETE');
    expect(init?.body).toBeUndefined();
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
