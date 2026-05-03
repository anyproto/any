import { type ReactElement } from 'react';
import { render } from '@testing-library/react';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

export function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Infinity },
      mutations: { retry: false },
    },
  });
}

export function renderWithProviders(
  ui: ReactElement,
  {
    store = createStore(),
    queryClient = createTestQueryClient(),
    initializeStore,
    initializeQueryClient,
  }: {
    store?: ReturnType<typeof createStore>;
    queryClient?: QueryClient;
    initializeStore?: (store: ReturnType<typeof createStore>) => void;
    initializeQueryClient?: (queryClient: QueryClient) => void;
  } = {},
) {
  initializeStore?.(store);
  initializeQueryClient?.(queryClient);

  return {
    ...render(
      <QueryClientProvider client={queryClient}>
        <Provider store={store}>{ui}</Provider>
      </QueryClientProvider>,
    ),
    store,
    queryClient,
  };
}
