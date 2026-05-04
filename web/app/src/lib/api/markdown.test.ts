import { describe, it, expect, beforeEach, vi } from 'vitest';
import { act, renderHook, waitFor, type RenderHookOptions } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createElement } from 'react';
import {
  getObjectMarkdown,
  markdownKeys,
  setObjectMarkdown,
  useObjectMarkdown,
  useSaveObjectMarkdown,
} from './markdown';

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

  it('optimistically updates the markdown cache when a save starts', async () => {
    let resolveFetch: (response: Response) => void = () => undefined;
    vi.spyOn(globalThis, 'fetch').mockImplementation(
      () =>
        new Promise<Response>((resolve) => {
          resolveFetch = resolve;
        }),
    );

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const queryKey = markdownKeys.one('spc-a', 'obj-1');
    qc.setQueryData(queryKey, 'old');
    const wrapper: RenderHookOptions<unknown>['wrapper'] = ({ children }) =>
      createElement(QueryClientProvider, { client: qc }, children);
    const { result } = renderHook(() => useSaveObjectMarkdown(), { wrapper });

    act(() => {
      result.current.mutate({
        spaceId: 'spc-a',
        objectId: 'obj-1',
        content: '- [ ]',
      });
    });

    await waitFor(() => expect(qc.getQueryData(queryKey)).toBe('- [ ]'));

    resolveFetch(
      new Response(
        JSON.stringify({ inserted: ['a'], updated: [], deleted: [], unchanged: 0 }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  it('uses cached markdown as session-authoritative data on remount', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ content: 'server-old' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    qc.setQueryData(markdownKeys.one('spc-a', 'obj-1'), '- [ ]');
    const wrapper: RenderHookOptions<unknown>['wrapper'] = ({ children }) =>
      createElement(QueryClientProvider, { client: qc }, children);

    const { result } = renderHook(() => useObjectMarkdown('spc-a', 'obj-1'), {
      wrapper,
    });

    expect(result.current.data).toBe('- [ ]');
    expect(fetchSpy).not.toHaveBeenCalled();
  });
});
