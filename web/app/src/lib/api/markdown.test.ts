import { describe, it, expect, beforeEach, vi } from 'vitest';
import { getObjectMarkdown, setObjectMarkdown } from './markdown';

describe('markdown API', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('GET decodes {content}', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ content: '# hi' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const md = await getObjectMarkdown('spc-a', 'obj-1');
    expect(md).toBe('# hi');
  });

  it('GET returns empty string when content is missing', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    expect(await getObjectMarkdown('spc-a', 'obj-1')).toBe('');
  });

  it('PUT sends {content} as JSON', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ inserted: [], updated: [], deleted: [], unchanged: 0 }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    await setObjectMarkdown('spc-a', 'obj-1', '# new');
    const [url, init] = fetchSpy.mock.calls[0]!;
    expect(url).toBe('/v1/spaces/spc-a/objects/obj-1/markdown');
    expect(init?.method).toBe('PUT');
    expect(init?.body).toBe(JSON.stringify({ content: '# new' }));
  });

  it('PUT returns the diff result', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({ inserted: ['a'], updated: ['b'], deleted: [], unchanged: 1 }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const res = await setObjectMarkdown('spc-a', 'obj-1', 'x');
    expect(res).toEqual({ inserted: ['a'], updated: ['b'], deleted: [], unchanged: 1 });
  });
});
