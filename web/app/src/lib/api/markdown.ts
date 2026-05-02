import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from './client';

/**
 * Wire shape mirrors the server handler in
 * internal/server/handlers_markdown.go.
 */
interface MarkdownGetResponse {
  content: string;
}

export interface MarkdownSetResponse {
  inserted: string[];
  updated: string[];
  deleted: string[];
  unchanged: number;
}

const KEYS = {
  one: (spaceId: string, objectId: string) =>
    ['markdown', spaceId, objectId] as const,
};

// ---------------- Plain functions -----------------------------------

export async function getObjectMarkdown(
  spaceId: string,
  objectId: string,
  signal?: AbortSignal,
): Promise<string> {
  const opts: { signal?: AbortSignal } = {};
  if (signal) opts.signal = signal;
  const res = await apiFetch<MarkdownGetResponse>(
    `/spaces/${encodeURIComponent(spaceId)}/objects/${encodeURIComponent(objectId)}/markdown`,
    opts,
  );
  return res.content ?? '';
}

export async function setObjectMarkdown(
  spaceId: string,
  objectId: string,
  content: string,
): Promise<MarkdownSetResponse> {
  return apiFetch<MarkdownSetResponse>(
    `/spaces/${encodeURIComponent(spaceId)}/objects/${encodeURIComponent(objectId)}/markdown`,
    { method: 'PUT', json: { content } },
  );
}

// ---------------- React hooks ---------------------------------------

/**
 * Read an object's markdown. Per-(space,object) cache key — switching
 * objects fetches fresh; switching back is cached.
 *
 * staleTime is 0 here because the editor manages its own dirty state
 * and we explicitly invalidate on save success. Refetch-on-focus is
 * disabled for the same reason — refetching while the user is typing
 * would clobber unsaved input.
 */
export function useObjectMarkdown(spaceId: string | null, objectId: string | null) {
  return useQuery({
    queryKey:
      spaceId && objectId
        ? KEYS.one(spaceId, objectId)
        : (['markdown', '__none__'] as const),
    queryFn: ({ signal }) => getObjectMarkdown(spaceId!, objectId!, signal),
    enabled: spaceId != null && objectId != null,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
}

interface SaveArgs {
  spaceId: string;
  objectId: string;
  content: string;
}

/**
 * Save an object's markdown. Updates the cache to the saved value on
 * success so a tab refocus reads stale-but-correct.
 */
export function useSaveObjectMarkdown() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ spaceId, objectId, content }: SaveArgs) =>
      setObjectMarkdown(spaceId, objectId, content),
    onSuccess: (_res, { spaceId, objectId, content }) => {
      qc.setQueryData(KEYS.one(spaceId, objectId), content);
    },
  });
}
