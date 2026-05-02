/* eslint-disable @typescript-eslint/no-explicit-any */
import { useCallback, useEffect, useReducer, useRef } from 'react';
import { useAtomValue } from 'jotai';
import type { Block } from '@blocknote/core';
import { BlockNoteView } from '@blocknote/mantine';
import { useCreateBlockNote } from '@blocknote/react';
import { useObjectMarkdown, useSaveObjectMarkdown } from '@/lib/api/markdown';
import { ApiError } from '@/lib/api/client';
import { resolvedThemeAtom } from '@/atoms/theme';
import { reduce, initial, type SaveState } from './saveMachine';
import './blocknote-theme.css';

const SAVE_DEBOUNCE_MS = 800;

interface Props {
  spaceId: string;
  objectId: string;
  /** Reports save state up to the header so it can render the indicator. */
  onStateChange?: (state: SaveState) => void;
}

/**
 * BlockNote-backed markdown editor for one object.
 *
 * Lifecycle:
 *  - On mount / objectId change: GET .../markdown, parse to blocks,
 *    seed the editor.
 *  - On editor change: convert blocks → markdown, push into the save
 *    machine; debounced flush triggers a PUT.
 *  - On unmount: best-effort flush of pending content (mutateAsync).
 */
export function MarkdownEditor({ spaceId, objectId, onStateChange }: Props) {
  const theme = useAtomValue(resolvedThemeAtom);
  // The `as any` is a strict-mode escape hatch — BlockNote's default
  // schema types collide with TS `exactOptionalPropertyTypes: true`.
  // Runtime is fine; only the type relationship is the issue.
  const editor = useCreateBlockNote() as any;

  const [state, dispatch] = useReducer(reduce, initial);
  const stateRef = useRef(state);
  stateRef.current = state;

  // Surface state to parent for the header indicator.
  useEffect(() => {
    onStateChange?.(state);
  }, [state, onStateChange]);

  // Initial load via TanStack Query.
  const loadQuery = useObjectMarkdown(spaceId, objectId);
  const saveMutation = useSaveObjectMarkdown();

  // Hydrate the editor when content arrives.
  useEffect(() => {
    let cancelled = false;
    if (loadQuery.isError) {
      const err =
        loadQuery.error instanceof ApiError
          ? loadQuery.error
          : new ApiError(
              { code: 'unknown', message: String(loadQuery.error) },
              0,
            );
      dispatch({ type: 'load_failed', error: err });
      return;
    }
    if (!loadQuery.isSuccess) return;
    void (async () => {
      const blocks = await editor.tryParseMarkdownToBlocks(loadQuery.data);
      if (cancelled) return;
      editor.replaceBlocks(editor.document, blocks as Block[]);
      dispatch({ type: 'load_ok', content: loadQuery.data });
    })();
    return () => {
      cancelled = true;
    };
  }, [editor, loadQuery.isSuccess, loadQuery.isError, loadQuery.data, loadQuery.error]);

  // Debounced flush. The doSave ref breaks the otherwise-circular
  // dep between scheduleFlush and doSave (each refers to the other).
  const flushTimerRef = useRef<number | null>(null);
  const doSaveRef = useRef<() => Promise<void>>(() => Promise.resolve());
  const scheduleFlush = useCallback(() => {
    if (flushTimerRef.current != null) {
      window.clearTimeout(flushTimerRef.current);
    }
    flushTimerRef.current = window.setTimeout(() => {
      flushTimerRef.current = null;
      void doSaveRef.current();
    }, SAVE_DEBOUNCE_MS);
  }, []);

  const doSave = useCallback(async () => {
    const s = stateRef.current;
    if (s.kind !== 'dirty' && s.kind !== 'save_error') return;
    const content = s.nextContent;
    dispatch({ type: 'flush' });
    try {
      await saveMutation.mutateAsync({ spaceId, objectId, content });
      dispatch({ type: 'save_ok' });
      // If the user typed during the flight, schedule the next save.
      if (stateRef.current.kind === 'dirty') {
        scheduleFlush();
      }
    } catch (err) {
      const apiErr =
        err instanceof ApiError
          ? err
          : new ApiError({ code: 'unknown', message: String(err) }, 0);
      dispatch({ type: 'save_failed', error: apiErr });
    }
  }, [spaceId, objectId, saveMutation, scheduleFlush]);
  doSaveRef.current = doSave;

  // onChange from BlockNote → state machine + debounce.
  const handleChange = useCallback(() => {
    void (async () => {
      const md = await editor.blocksToMarkdownLossy(editor.document);
      dispatch({ type: 'edit', content: md, now: Date.now() });
      scheduleFlush();
    })();
  }, [editor, scheduleFlush]);

  // Best-effort flush on unmount (object switch).
  useEffect(() => {
    return () => {
      if (flushTimerRef.current != null) {
        window.clearTimeout(flushTimerRef.current);
        flushTimerRef.current = null;
      }
      const s = stateRef.current;
      if (s.kind === 'dirty' || s.kind === 'save_error') {
        // Fire-and-forget. The mutation runs in the background; if the
        // app stays open we still see toast errors via the mutation's
        // own onError. (PR #6 doesn't surface them — that's a follow-up.)
        void saveMutation.mutateAsync({
          spaceId,
          objectId,
          content: s.kind === 'dirty' ? s.nextContent : s.nextContent,
        });
      }
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spaceId, objectId]);

  if (loadQuery.isError) {
    const code = loadQuery.error instanceof ApiError ? loadQuery.error.code : 'unknown';
    const message =
      loadQuery.error instanceof Error ? loadQuery.error.message : 'Failed to load';
    return (
      <div className="mx-auto max-w-2xl px-8 py-10">
        <div
          role="alert"
          className="rounded-md border border-destructive/30 bg-destructive/[0.06] p-4 text-sm"
        >
          <p className="font-medium text-destructive">Couldn&rsquo;t load this object</p>
          <p className="mt-1 text-foreground/70">
            <code className="font-mono">{code}</code> — {message}
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-2xl px-8 py-6">
      <BlockNoteView editor={editor} theme={theme} onChange={handleChange} />
    </div>
  );
}
