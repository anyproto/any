import { QueryClient } from '@tanstack/react-query';

/**
 * Shared QueryClient. Conservative defaults for v1:
 * - 30 s staleTime: avoids over-fetching while we don't have /subscribe.
 * - retry once on failure: most errors here are server-side and a retry
 *   either resolves them or it doesn't — exponential retry is overkill
 *   for a localhost API.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: true,
    },
    mutations: {
      retry: 0,
    },
  },
});
