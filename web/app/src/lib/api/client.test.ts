import { describe, it, expect, beforeEach, vi } from 'vitest';
import { apiFetch, ApiError } from './client';

describe('apiFetch', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('returns parsed JSON on 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const data = await apiFetch<{ ok: boolean }>('/health');
    expect(data).toEqual({ ok: true });
  });

  it('decodes the canonical error envelope on 4xx', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          error: { code: 'space.not_found', message: 'no such space' },
        }),
        { status: 404, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    await expect(apiFetch('/spaces/nope')).rejects.toMatchObject({
      name: 'ApiError',
      code: 'space.not_found',
      status: 404,
      message: 'no such space',
    });
  });

  it('preserves error.details', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          error: { code: 'request.bad_json', message: 'oops', details: { field: 'name' } },
        }),
        { status: 400, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    try {
      await apiFetch('/x');
      expect.unreachable();
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).details).toEqual({ field: 'name' });
    }
  });

  it('throws client.bad_response when error body is non-conforming', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ wrong: 'shape' }), {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    await expect(apiFetch('/x')).rejects.toMatchObject({
      code: 'client.bad_response',
      status: 500,
    });
  });

  it('throws client.bad_response when body is not JSON', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('not json at all', { status: 502 }),
    );
    await expect(apiFetch('/x')).rejects.toMatchObject({
      code: 'client.bad_response',
      status: 502,
    });
  });

  it('serializes JSON body and sets Content-Type', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    await apiFetch('/spaces', { method: 'POST', json: { name: 'demo' } });
    const [, init] = fetchSpy.mock.calls[0]!;
    expect(init?.body).toBe(JSON.stringify({ name: 'demo' }));
    const ct = (init?.headers as Headers).get('Content-Type');
    expect(ct).toBe('application/json; charset=utf-8');
  });
});
