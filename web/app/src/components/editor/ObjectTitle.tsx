import { useEffect, useRef, useState } from 'react';
import { useSetAtom } from 'jotai';
import { useObjectName, useRenameObjectAnywhere } from '@/lib/api/objects';
import { clearObjectTitleDraftAtom, setObjectTitleDraftAtom } from '@/atoms';
import { ApiError } from '@/lib/api/client';
import { toast } from '@/components/ui';
import { cn } from '@/lib/cn';

interface Props {
  spaceId: string;
  objectId: string;
}

/**
 * Notion-style inline title for the open object. Sits above the
 * BlockNote body, reads `any.name`, commits on Enter or blur.
 *
 * The component owns local draft state so typing doesn't fight the
 * cached value. The cache is the source of truth: when the query
 * returns or refetches (e.g. tree rename), we sync the draft.
 */
export function ObjectTitle({ spaceId, objectId }: Props) {
  const nameQuery = useObjectName(spaceId, objectId);
  const rename = useRenameObjectAnywhere(spaceId);
  const setLiveDraft = useSetAtom(setObjectTitleDraftAtom);
  const clearLiveDraft = useSetAtom(clearObjectTitleDraftAtom);
  const ref = useRef<HTMLTextAreaElement>(null);

  // Local draft. We keep it as a string (not undefined) so React
  // treats the textarea as controlled across the whole lifecycle.
  const [draft, setDraft] = useState<string>(() => nameQuery.data ?? '');
  // Track which object id the draft corresponds to so a fast object
  // switch doesn't carry a stale draft into the new object.
  const syncedFor = useRef<{ objectId: string; value: string } | null>(null);

  useEffect(() => {
    if (!nameQuery.isSuccess) return;
    const current = nameQuery.data ?? '';
    const synced = syncedFor.current;
    // Sync if the cached value changed under us (rename from tree),
    // or if the object switched.
    if (
      synced == null ||
      synced.objectId !== objectId ||
      synced.value !== current
    ) {
      setDraft(current);
      clearLiveDraft({ spaceId, objectId });
      syncedFor.current = { objectId, value: current };
    }
  }, [clearLiveDraft, objectId, nameQuery.isSuccess, nameQuery.data, spaceId]);

  // Auto-grow the textarea so multi-line titles wrap cleanly.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight}px`;
  }, [draft]);

  const commit = async () => {
    const trimmed = draft.replace(/\s+/g, ' ').trim();
    const current = (nameQuery.data ?? '').trim();
    if (trimmed === current) {
      clearLiveDraft({ spaceId, objectId });
      return;
    }
    try {
      await rename.mutateAsync({ objectId, name: trimmed });
      syncedFor.current = { objectId, value: trimmed };
      clearLiveDraft({ spaceId, objectId });
    } catch (err) {
      const code = err instanceof ApiError ? err.code : 'unknown';
      const msg = err instanceof Error ? err.message : 'Failed to rename';
      toast.error(`${code}: ${msg}`);
      // Roll back the draft on failure so the user sees what's saved.
      setDraft(current);
      clearLiveDraft({ spaceId, objectId });
    }
  };

  return (
    <div className="mb-3">
      <textarea
        ref={ref}
        rows={1}
        value={draft}
        placeholder="Untitled"
        spellCheck={false}
        aria-label="Object title"
        onChange={(e) => {
          const next = e.target.value;
          setDraft(next);
          setLiveDraft({ spaceId, objectId, title: next });
        }}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            void commit();
            ref.current?.blur();
          } else if (e.key === 'Escape') {
            e.preventDefault();
            setDraft(nameQuery.data ?? '');
            clearLiveDraft({ spaceId, objectId });
            ref.current?.blur();
          }
        }}
        onBlur={() => {
          void commit();
        }}
        className={cn(
          'block w-full resize-none border-0 bg-transparent p-0',
          // Anytype 1:1 — _vars.scss:34-36 → 36 / 40 / 700.
          'text-[36px] font-bold leading-[40px] tracking-[-0.2px] text-foreground',
          'placeholder:text-foreground/25',
          'focus-visible:outline-none focus-visible:ring-0',
        )}
      />
    </div>
  );
}
