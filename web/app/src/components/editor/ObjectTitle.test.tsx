import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectTitle } from './ObjectTitle';
import { objectTitleDraftKey, objectTitleDraftsAtom } from '@/atoms';
import { objectKeys, type ObjectRecord } from '@/lib/api/objects';

interface FetchCall {
  url: string;
  method: string;
  body?: string;
}

function setup(
  initialName = 'Lasagna',
  configureQueryClient?: (qc: QueryClient) => void,
) {
  const calls: FetchCall[] = [];
  vi.spyOn(globalThis, 'fetch').mockImplementation((input, init) => {
    const url =
      typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? 'GET').toUpperCase();
    const body = typeof init?.body === 'string' ? init.body : undefined;
    calls.push({ url, method, body });

    // /v1/spaces/:s/objects/query → name lookup
    if (url.endsWith('/objects/query') && method === 'POST') {
      return Promise.resolve(
        new Response(
          JSON.stringify({
            records: [{ id: 'obj-1', any: { name: initialName } }],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
      );
    }
    // setObjectProperty (rename) → ack
    return Promise.resolve(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
  });

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  configureQueryClient?.(qc);
  const store = createStore();
  const utils = render(
    <QueryClientProvider client={qc}>
      <Provider store={store}>
        <ObjectTitle spaceId="spc-test" objectId="obj-1" />
      </Provider>
    </QueryClientProvider>,
  );
  return { ...utils, calls, store };
}

describe('<ObjectTitle>', () => {
  it('renders the name from the query', async () => {
    setup('Lasagna');
    const tb = await screen.findByRole<HTMLTextAreaElement>('textbox', {
      name: /object title/i,
    });
    await waitFor(() => expect(tb.value).toBe('Lasagna'));
  });

  it('shows the Untitled placeholder when the name is empty', async () => {
    setup('');
    const tb = await screen.findByRole<HTMLTextAreaElement>('textbox', {
      name: /object title/i,
    });
    await waitFor(() => expect(tb.value).toBe(''));
    expect(tb.placeholder).toBe('Untitled');
  });

  it('uses a cached tree title on the first render', () => {
    setup('Server title', (qc) => {
      qc.setQueryData<ObjectRecord[]>(objectKeys.childrenOf('spc-test', ''), [
        { id: 'obj-1', any: { name: 'Cached title' } },
      ]);
    });

    expect(
      screen.getByRole<HTMLTextAreaElement>('textbox', { name: /object title/i }).value,
    ).toBe('Cached title');
  });

  it('Enter commits a rename via setObjectProperty', async () => {
    const { calls, store } = setup('Old name');
    const tb = await screen.findByRole<HTMLTextAreaElement>('textbox', {
      name: /object title/i,
    });
    await waitFor(() => expect(tb.value).toBe('Old name'));
    await userEvent.clear(tb);
    await userEvent.type(tb, 'New name');
    expect(store.get(objectTitleDraftsAtom)[objectTitleDraftKey('spc-test', 'obj-1')]).toBe(
      'New name',
    );
    await userEvent.type(tb, '{enter}');
    await waitFor(() => {
      const rename = calls.find(
        (c) =>
          c.method === 'POST' &&
          c.url === '/v1/spaces/spc-test/properties/obj-1/base/any',
      );
      expect(rename?.body).toBe(JSON.stringify({ patch: { name: 'New name' } }));
    });
    await waitFor(() => {
      expect(
        store.get(objectTitleDraftsAtom)[objectTitleDraftKey('spc-test', 'obj-1')],
      ).toBeUndefined();
    });
  });

  it('Escape rolls back to the cached name', async () => {
    setup('Original');
    const tb = await screen.findByRole<HTMLTextAreaElement>('textbox', {
      name: /object title/i,
    });
    await waitFor(() => expect(tb.value).toBe('Original'));
    await userEvent.clear(tb);
    await userEvent.type(tb, 'Half-typed{escape}');
    await waitFor(() => expect(tb.value).toBe('Original'));
  });
});
