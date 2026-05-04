import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import {
  Check,
  Code2,
  Heading1,
  Heading2,
  List,
  ListOrdered,
  Pilcrow,
  Quote,
} from 'lucide-react';
import {
  $createParagraphNode,
  $getRoot,
  $getSelection,
  $isRangeSelection,
  $isTextNode,
  type EditorState,
  type LexicalEditor,
} from 'lexical';
import { CodeHighlightNode, CodeNode, $createCodeNode } from '@lexical/code';
import { AutoLinkNode, LinkNode } from '@lexical/link';
import {
  INSERT_CHECK_LIST_COMMAND,
  INSERT_ORDERED_LIST_COMMAND,
  INSERT_UNORDERED_LIST_COMMAND,
  ListItemNode,
  ListNode,
} from '@lexical/list';
import {
  $convertFromMarkdownString,
  $convertToMarkdownString,
} from '@lexical/markdown';
import { LexicalComposer } from '@lexical/react/LexicalComposer';
import { CheckListPlugin } from '@lexical/react/LexicalCheckListPlugin';
import { ContentEditable } from '@lexical/react/LexicalContentEditable';
import { LexicalErrorBoundary } from '@lexical/react/LexicalErrorBoundary';
import { HistoryPlugin } from '@lexical/react/LexicalHistoryPlugin';
import { LinkPlugin } from '@lexical/react/LexicalLinkPlugin';
import { ListPlugin } from '@lexical/react/LexicalListPlugin';
import { MarkdownShortcutPlugin } from '@lexical/react/LexicalMarkdownShortcutPlugin';
import { OnChangePlugin } from '@lexical/react/LexicalOnChangePlugin';
import { RichTextPlugin } from '@lexical/react/LexicalRichTextPlugin';
import { useLexicalComposerContext } from '@lexical/react/LexicalComposerContext';
import { $setBlocksType } from '@lexical/selection';
import {
  $createHeadingNode,
  $createQuoteNode,
  HeadingNode,
  QuoteNode,
} from '@lexical/rich-text';
import { ApiError } from '@/lib/api/client';
import { useObjectMarkdown, useSaveObjectMarkdown } from '@/lib/api/markdown';
import { keyOf } from '@/shared';
import { OrphanCleanup } from './OrphanCleanup';
import { ObjectTitle } from './ObjectTitle';
import { ObjectTypeBar } from './ObjectTypeBar';
import { LexicalBlockMenu } from './LexicalBlockMenu';
import { LexicalSelectionMenu } from './LexicalSelectionMenu';
import { shouldHydrateMarkdownEditor } from './editorHydration';
import { MARKDOWN_TRANSFORMERS } from './lexicalMarkdownTransformers';
import { initial, reduce, type SaveState } from './saveMachine';
import './lexical-theme.css';

const SAVE_DEBOUNCE_MS = 800;

interface Props {
  spaceId: string;
  objectId: string;
  onStateChange?: (state: SaveState) => void;
}

const lexicalTheme = {
  paragraph: 'lexical-paragraph',
  heading: {
    h1: 'lexical-heading lexical-heading-h1',
    h2: 'lexical-heading lexical-heading-h2',
    h3: 'lexical-heading lexical-heading-h3',
  },
  list: {
    ul: 'lexical-list lexical-list-ul',
    ol: 'lexical-list lexical-list-ol',
    listitem: 'lexical-list-item',
    nested: {
      listitem: 'lexical-list-item-nested',
    },
    checklist: 'lexical-list lexical-check-list',
    listitemChecked: 'lexical-check-list-item lexical-check-list-item-checked',
    listitemUnchecked: 'lexical-check-list-item lexical-check-list-item-unchecked',
  },
  quote: 'lexical-quote',
  code: 'lexical-code-block',
  text: {
    bold: 'lexical-text-bold',
    italic: 'lexical-text-italic',
    underline: 'lexical-text-underline',
    strikethrough: 'lexical-text-strikethrough',
    code: 'lexical-text-code',
    subscript: 'lexical-text-subscript',
    superscript: 'lexical-text-superscript',
  },
};

const lexicalNodes = [
  HeadingNode,
  QuoteNode,
  ListNode,
  ListItemNode,
  CodeNode,
  CodeHighlightNode,
  LinkNode,
  AutoLinkNode,
];

export function LexicalMarkdownEditor({ spaceId, objectId, onStateChange }: Props) {
  const [state, dispatch] = useReducer(reduce, initial);
  const [editorAnchorElem, setEditorAnchorElem] = useState<HTMLElement | null>(null);
  const stateRef = useRef(state);
  stateRef.current = state;

  useEffect(() => {
    onStateChange?.(state);
  }, [state, onStateChange]);

  const loadQuery = useObjectMarkdown(spaceId, objectId);
  const saveMutation = useSaveObjectMarkdown();
  const pendingContentRef = useRef<string | null>(null);

  useEffect(() => {
    if (!loadQuery.isError) return;
    const err =
      loadQuery.error instanceof ApiError
        ? loadQuery.error
        : new ApiError({ code: 'unknown', message: String(loadQuery.error) }, 0);
    dispatch({ type: 'load_failed', error: err });
  }, [loadQuery.error, loadQuery.isError]);

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
    const content =
      s.kind === 'dirty' || s.kind === 'save_error'
        ? s.nextContent
        : pendingContentRef.current;
    if (content == null) return;
    if (s.kind === 'dirty' || s.kind === 'save_error') {
      dispatch({ type: 'flush' });
    }
    try {
      await saveMutation.mutateAsync({ spaceId, objectId, content });
      if (pendingContentRef.current === content) {
        pendingContentRef.current = null;
      }
      if (stateRef.current.kind === 'saving') {
        dispatch({ type: 'save_ok' });
      }
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
  }, [objectId, saveMutation, scheduleFlush, spaceId]);
  doSaveRef.current = doSave;

  const handleMarkdownChange = useCallback(
    (content: string) => {
      const current = stateRef.current;
      const savedContent =
        'savedContent' in current ? current.savedContent : loadQuery.data;
      if (content === (savedContent ?? '')) {
        pendingContentRef.current = null;
        return;
      }
      pendingContentRef.current = content;
      dispatch({ type: 'edit', content, now: Date.now() });
      scheduleFlush();
    },
    [loadQuery.data, scheduleFlush],
  );

  useEffect(() => {
    return () => {
      if (flushTimerRef.current != null) {
        window.clearTimeout(flushTimerRef.current);
        flushTimerRef.current = null;
      }
      const s = stateRef.current;
      const content =
        s.kind === 'dirty' || s.kind === 'save_error'
          ? s.nextContent
          : pendingContentRef.current;
      if (content != null) {
        void saveMutation.mutateAsync({
          spaceId,
          objectId,
          content,
        });
      }
    };
    // Intentionally keyed only to object identity: refs provide latest dirty state/timer,
    // while mutation object rebinding would turn hook churn into a false unmount save.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spaceId, objectId]);

  const editorConfig = useMemo(
    () => ({
      namespace: keyOf('any-lexical-editor', spaceId, objectId),
      theme: lexicalTheme,
      nodes: lexicalNodes,
      onError(error: Error) {
        throw error;
      },
    }),
    [objectId, spaceId],
  );

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
          <p className="mt-3 text-xs text-foreground/60">
            If this is an orphan row (the underlying tree was deleted but the list entry
            survives), removing it from the list is safe — see{' '}
            <code className="font-mono">docs/03-api.md</code> § Object deletion.
          </p>
          <div className="mt-3">
            <OrphanCleanup spaceId={spaceId} objectId={objectId} />
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-[704px] px-8 py-6">
      <ObjectTitle spaceId={spaceId} objectId={objectId} />
      <ObjectTypeBar spaceId={spaceId} objectId={objectId} />
      <div className="lexical-editor-hover-frame">
        <div ref={setEditorAnchorElem} className="anytype-lexical-editor">
          <LexicalComposer initialConfig={editorConfig}>
            <LexicalHydrationPlugin
              identityKey={keyOf('lexical-hydration', spaceId, objectId)}
              markdown={loadQuery.isSuccess ? loadQuery.data : null}
              onHydrated={(content) => dispatch({ type: 'load_ok', content })}
            />
            <RichTextPlugin
              contentEditable={
                <ContentEditable
                  className="ContentEditable__root lexical-content-editable"
                  aria-placeholder="Write, or press / for commands"
                  placeholder={
                    <div className="lexical-placeholder">
                      Write, or press / for commands
                    </div>
                  }
                />
              }
              ErrorBoundary={LexicalErrorBoundary}
            />
            <HistoryPlugin />
            <ListPlugin />
            <CheckListPlugin />
            <LinkPlugin />
            <MarkdownShortcutPlugin transformers={MARKDOWN_TRANSFORMERS} />
            <LexicalMarkdownChangePlugin onMarkdownChange={handleMarkdownChange} />
            {editorAnchorElem ? <LexicalBlockMenu anchorElem={editorAnchorElem} /> : null}
            <LexicalSelectionMenu />
            <LexicalSlashMenu />
          </LexicalComposer>
        </div>
      </div>
    </div>
  );
}

function LexicalHydrationPlugin({
  identityKey,
  markdown,
  onHydrated,
}: {
  identityKey: string;
  markdown: string | null;
  onHydrated: (content: string) => void;
}) {
  const [editor] = useLexicalComposerContext();
  const hydratedIdentityRef = useRef<string | null>(null);

  useEffect(() => {
    if (markdown == null) return;
    if (
      !shouldHydrateMarkdownEditor({
        hydratedIdentity: hydratedIdentityRef.current,
        identityKey,
        markdown,
      })
    ) {
      return;
    }
    // Import only when opening a different object. Save mutations update the
    // markdown query cache for this same object, and re-importing that cache
    // would rebuild the Lexical tree and move the user's cursor to the start.
    editor.update(() => {
      $getRoot().clear();
      $convertFromMarkdownString(markdown, MARKDOWN_TRANSFORMERS);
    });
    hydratedIdentityRef.current = identityKey;
    onHydrated(markdown);
  }, [editor, identityKey, markdown, onHydrated]);

  return null;
}

function LexicalMarkdownChangePlugin({
  onMarkdownChange,
}: {
  onMarkdownChange: (markdown: string) => void;
}) {
  const handleChange = useCallback(
    (editorState: EditorState) => {
      editorState.read(() => {
        onMarkdownChange($convertToMarkdownString(MARKDOWN_TRANSFORMERS));
      });
    },
    [onMarkdownChange],
  );

  return (
    <OnChangePlugin
      ignoreHistoryMergeTagChange
      ignoreSelectionChange
      onChange={handleChange}
    />
  );
}

type BlockKind = 'paragraph' | 'h1' | 'h2' | 'quote' | 'code';

function setBlock(editor: LexicalEditor, kind: BlockKind) {
  editor.update(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) return;
    if (kind === 'paragraph') {
      $setBlocksType(selection, () => $createParagraphNode());
    } else if (kind === 'quote') {
      $setBlocksType(selection, () => $createQuoteNode());
    } else if (kind === 'code') {
      $setBlocksType(selection, () => $createCodeNode());
    } else {
      $setBlocksType(selection, () => $createHeadingNode(kind));
    }
  });
}

interface SlashCommand {
  id: string;
  title: string;
  subtitle: string;
  icon: ReactNode;
  run: (editor: LexicalEditor) => void;
}

const SLASH_COMMANDS: readonly SlashCommand[] = [
  {
    id: 'paragraph',
    title: 'Paragraph',
    subtitle: 'Plain text block',
    icon: <Pilcrow className="h-4 w-4" />,
    run: (editor) => setBlock(editor, 'paragraph'),
  },
  {
    id: 'h1',
    title: 'Heading 1',
    subtitle: 'Top-level heading',
    icon: <Heading1 className="h-4 w-4" />,
    run: (editor) => setBlock(editor, 'h1'),
  },
  {
    id: 'h2',
    title: 'Heading 2',
    subtitle: 'Section heading',
    icon: <Heading2 className="h-4 w-4" />,
    run: (editor) => setBlock(editor, 'h2'),
  },
  {
    id: 'bullet',
    title: 'Bullet List',
    subtitle: 'List with unordered items',
    icon: <List className="h-4 w-4" />,
    run: (editor) => editor.dispatchCommand(INSERT_UNORDERED_LIST_COMMAND, undefined),
  },
  {
    id: 'number',
    title: 'Numbered List',
    subtitle: 'List with ordered items',
    icon: <ListOrdered className="h-4 w-4" />,
    run: (editor) => editor.dispatchCommand(INSERT_ORDERED_LIST_COMMAND, undefined),
  },
  {
    id: 'check',
    title: 'Check List',
    subtitle: 'List with checkboxes',
    icon: <Check className="h-4 w-4" />,
    run: (editor) => editor.dispatchCommand(INSERT_CHECK_LIST_COMMAND, undefined),
  },
  {
    id: 'quote',
    title: 'Quote',
    subtitle: 'Quote or excerpt',
    icon: <Quote className="h-4 w-4" />,
    run: (editor) => setBlock(editor, 'quote'),
  },
  {
    id: 'code',
    title: 'Code Block',
    subtitle: 'Preformatted text',
    icon: <Code2 className="h-4 w-4" />,
    run: (editor) => setBlock(editor, 'code'),
  },
];

function LexicalSlashMenu() {
  const [editor] = useLexicalComposerContext();
  const [menu, setMenu] = useState<{
    open: boolean;
    query: string;
    left: number;
    top: number;
  }>({ open: false, query: '', left: 0, top: 0 });

  useEffect(() => {
    return editor.registerUpdateListener(({ editorState }) => {
      editorState.read(() => {
        const selection = $getSelection();
        if (!$isRangeSelection(selection) || !selection.isCollapsed()) {
          setMenu((prev) => (prev.open ? { ...prev, open: false } : prev));
          return;
        }
        const anchor = selection.anchor;
        const node = anchor.getNode();
        if (!$isTextNode(node)) {
          setMenu((prev) => (prev.open ? { ...prev, open: false } : prev));
          return;
        }
        const before = node.getTextContent().slice(0, anchor.offset);
        const match = before.match(/(?:^|\s)\/([a-z0-9]*)$/i);
        if (!match) {
          setMenu((prev) => (prev.open ? { ...prev, open: false } : prev));
          return;
        }
        const nativeSelection = window.getSelection();
        const range = nativeSelection?.rangeCount ? nativeSelection.getRangeAt(0) : null;
        const rect = range?.getBoundingClientRect();
        setMenu({
          open: true,
          query: match[1] ?? '',
          left: Math.max(16, rect?.left ?? 16),
          top: Math.max(16, (rect?.bottom ?? 0) + 10),
        });
      });
    });
  }, [editor]);

  useEffect(() => {
    if (!menu.open) return undefined;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setMenu((prev) => ({ ...prev, open: false }));
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [menu.open]);

  if (!menu.open) return null;

  const commands = SLASH_COMMANDS.filter((command) =>
    command.title.toLowerCase().includes(menu.query.toLowerCase()),
  );

  return createPortal(
    <div
      className="lexical-slash-menu"
      style={{ left: menu.left, top: menu.top }}
      role="listbox"
      aria-label="Block commands"
    >
      <div className="lexical-slash-menu-label">Basic blocks</div>
      {commands.map((command) => (
        <button
          key={command.id}
          type="button"
          className="lexical-slash-menu-item"
          role="option"
          aria-selected="false"
          onMouseDown={(event) => {
            event.preventDefault();
            removeSlashTrigger(editor);
            command.run(editor);
            setMenu((prev) => ({ ...prev, open: false }));
          }}
        >
          <span className="lexical-slash-menu-icon" aria-hidden>
            {command.icon}
          </span>
          <span className="lexical-slash-menu-copy">
            <span className="lexical-slash-menu-title">{command.title}</span>
            <span className="lexical-slash-menu-subtitle">{command.subtitle}</span>
          </span>
        </button>
      ))}
      {commands.length === 0 && (
        <div className="lexical-slash-menu-empty">No matching commands</div>
      )}
    </div>,
    document.body,
  );
}

function removeSlashTrigger(editor: LexicalEditor) {
  editor.update(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection) || !selection.isCollapsed()) return;
    const anchor = selection.anchor;
    const node = anchor.getNode();
    if (!$isTextNode(node)) return;
    const text = node.getTextContent();
    const before = text.slice(0, anchor.offset);
    const match = before.match(/(?:^|\s)\/([a-z0-9]*)$/i);
    if (!match) return;
    const matched = match[0];
    const slashIndex = matched.indexOf('/');
    const start = anchor.offset - matched.length + slashIndex;
    node.spliceText(start, anchor.offset - start, '', true);
  });
}
