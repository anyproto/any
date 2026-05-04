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

export const markdownKeys = {
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
 * Once loaded, markdown is locally authoritative for this app session.
 * The editor save mutation writes the cache optimistically; refetching
 * on every remount can race debounced/unmount saves and replace fresh
 * structural edits with older server content.
 */
export function useObjectMarkdown(spaceId: string | null, objectId: string | null) {
  return useQuery({
    queryKey:
      spaceId && objectId
        ? markdownKeys.one(spaceId, objectId)
        : (['markdown', '__none__'] as const),
    queryFn: ({ signal }) => getObjectMarkdown(spaceId!, objectId!, signal),
    enabled: spaceId != null && objectId != null,
    staleTime: Infinity,
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
    onMutate: async ({ spaceId, objectId, content }) => {
      const queryKey = markdownKeys.one(spaceId, objectId);
      await qc.cancelQueries({ queryKey });
      qc.setQueryData(queryKey, content);
    },
    onSuccess: (_res, { spaceId, objectId, content }) => {
      qc.setQueryData(markdownKeys.one(spaceId, objectId), content);
    },
  });
}
