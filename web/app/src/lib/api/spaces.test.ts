import { describe, it, expect, vi, beforeEach } from 'vitest';
import { listSpaces } from './spaces';

describe('listSpaces', () => {
  beforeEach(() => vi.restoreAllMocks());

  it('drops soft-deleted entries even if the server returns them', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          spaces: [
            { id: 'a', status: 'active', ownRole: 'owner', createdAt: '' },
            { id: 'b', status: 'deleted', ownRole: 'owner', createdAt: '' },
            { id: 'c', status: 'joining', ownRole: 'reader', createdAt: '' },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    const out = await listSpaces();
    expect(out.map((s) => s.id)).toEqual(['a', 'c']);
  });

  it('returns [] when the body has no spaces', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    expect(await listSpaces()).toEqual([]);
  });
});
