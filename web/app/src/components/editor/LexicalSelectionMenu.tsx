import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import {
  Bold,
  CaseLower,
  CaseSensitive,
  CaseUpper,
  Code2,
  Italic,
  Link2,
  RemoveFormatting,
  Strikethrough,
  Subscript,
  Superscript,
  Underline,
  Unlink2,
} from 'lucide-react';
import {
  $getSelection,
  $isRangeSelection,
  FORMAT_TEXT_COMMAND,
  type LexicalEditor,
  type TextFormatType,
} from 'lexical';
import { formatUrl, TOGGLE_LINK_COMMAND } from '@lexical/link';
import { useLexicalComposerContext } from '@lexical/react/LexicalComposerContext';

type Placement = 'above' | 'below';

interface MenuState {
  open: boolean;
  left: number;
  top: number;
  placement: Placement;
}

type SelectionMenuAction =
  | {
      kind: 'format';
      id: TextFormatType;
      label: string;
      icon: ReactNode;
    }
  | {
      kind: 'case';
      id: 'uppercase' | 'lowercase' | 'titlecase';
      label: string;
      icon: ReactNode;
    }
  | {
      kind: 'link' | 'unlink' | 'clear';
      id: string;
      label: string;
      icon: ReactNode;
    }
  | {
      kind: 'divider';
      id: string;
    };

type MenuButtonAction = Exclude<SelectionMenuAction, { kind: 'divider' }>;

const HIDDEN_MENU: MenuState = {
  open: false,
  left: 0,
  top: 0,
  placement: 'above',
};

const MENU_HALF_WIDTH = 236;

const SELECTION_MENU_ACTIONS: readonly SelectionMenuAction[] = [
  { kind: 'format', id: 'bold', label: 'Bold', icon: <Bold className="h-4 w-4" /> },
  {
    kind: 'format',
    id: 'italic',
    label: 'Italic',
    icon: <Italic className="h-4 w-4" />,
  },
  {
    kind: 'format',
    id: 'underline',
    label: 'Underline',
    icon: <Underline className="h-4 w-4" />,
  },
  {
    kind: 'format',
    id: 'strikethrough',
    label: 'Strikethrough',
    icon: <Strikethrough className="h-4 w-4" />,
  },
  {
    kind: 'format',
    id: 'code',
    label: 'Inline code',
    icon: <Code2 className="h-4 w-4" />,
  },
  { kind: 'divider', id: 'script-divider' },
  {
    kind: 'format',
    id: 'subscript',
    label: 'Subscript',
    icon: <Subscript className="h-4 w-4" />,
  },
  {
    kind: 'format',
    id: 'superscript',
    label: 'Superscript',
    icon: <Superscript className="h-4 w-4" />,
  },
  { kind: 'divider', id: 'case-divider' },
  {
    kind: 'case',
    id: 'uppercase',
    label: 'Uppercase',
    icon: <CaseUpper className="h-4 w-4" />,
  },
  {
    kind: 'case',
    id: 'lowercase',
    label: 'Lowercase',
    icon: <CaseLower className="h-4 w-4" />,
  },
  {
    kind: 'case',
    id: 'titlecase',
    label: 'Title case',
    icon: <CaseSensitive className="h-4 w-4" />,
  },
  { kind: 'divider', id: 'link-divider' },
  { kind: 'link', id: 'link', label: 'Add link', icon: <Link2 className="h-4 w-4" /> },
  {
    kind: 'unlink',
    id: 'unlink',
    label: 'Remove link',
    icon: <Unlink2 className="h-4 w-4" />,
  },
  { kind: 'divider', id: 'clear-divider' },
  {
    kind: 'clear',
    id: 'clear',
    label: 'Clear formatting',
    icon: <RemoveFormatting className="h-4 w-4" />,
  },
];

export function LexicalSelectionMenu() {
  const [editor] = useLexicalComposerContext();
  const [menu, setMenu] = useState<MenuState>(HIDDEN_MENU);
  const updateTimerRef = useRef<number | null>(null);

  const closeMenu = useCallback(() => {
    setMenu((current) => (current.open ? HIDDEN_MENU : current));
  }, []);

  const updateMenu = useCallback(() => {
    const rootElement = editor.getRootElement();
    const selection = window.getSelection();
    if (!rootElement || !isUsableSelection(selection, rootElement)) {
      closeMenu();
      return;
    }

    const rect = getSelectionRect(selection);
    if (!rect) {
      closeMenu();
      return;
    }

    const placement: Placement = rect.top > 62 ? 'above' : 'below';
    const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
    const left = clamp(
      rect.left + rect.width / 2,
      MENU_HALF_WIDTH + 12,
      Math.max(MENU_HALF_WIDTH + 12, viewportWidth - MENU_HALF_WIDTH - 12),
    );
    const top =
      placement === 'above'
        ? Math.max(14, rect.top - 10)
        : Math.min(window.innerHeight - 14, rect.bottom + 10);

    setMenu({ open: true, left, top, placement });
  }, [closeMenu, editor]);

  const scheduleUpdate = useCallback(() => {
    if (updateTimerRef.current != null) {
      window.clearTimeout(updateTimerRef.current);
    }
    updateTimerRef.current = window.setTimeout(() => {
      updateTimerRef.current = null;
      updateMenu();
    }, 0);
  }, [updateMenu]);

  useEffect(() => {
    const unregister = editor.registerUpdateListener(() => scheduleUpdate());

    const onSelectionChange = () => scheduleUpdate();
    const onMouseUp = () => scheduleUpdate();
    const onKeyUp = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        closeMenu();
        return;
      }
      scheduleUpdate();
    };
    const onScrollOrResize = () => scheduleUpdate();
    const onContextMenu = (event: MouseEvent) => {
      const rootElement = editor.getRootElement();
      const target = event.target;
      const selection = window.getSelection();
      if (
        !rootElement ||
        !(target instanceof Node) ||
        !rootElement.contains(target) ||
        !isUsableSelection(selection, rootElement)
      ) {
        return;
      }
      event.preventDefault();
      const viewportWidth = document.documentElement.clientWidth || window.innerWidth;
      setMenu({
        open: true,
        left: clamp(
          event.clientX,
          MENU_HALF_WIDTH + 12,
          Math.max(MENU_HALF_WIDTH + 12, viewportWidth - MENU_HALF_WIDTH - 12),
        ),
        top: Math.min(window.innerHeight - 14, event.clientY + 10),
        placement: 'below',
      });
    };

    document.addEventListener('selectionchange', onSelectionChange);
    document.addEventListener('mouseup', onMouseUp);
    document.addEventListener('keyup', onKeyUp);
    document.addEventListener('contextmenu', onContextMenu);
    window.addEventListener('resize', onScrollOrResize);
    window.addEventListener('scroll', onScrollOrResize, true);

    return () => {
      unregister();
      if (updateTimerRef.current != null) {
        window.clearTimeout(updateTimerRef.current);
      }
      document.removeEventListener('selectionchange', onSelectionChange);
      document.removeEventListener('mouseup', onMouseUp);
      document.removeEventListener('keyup', onKeyUp);
      document.removeEventListener('contextmenu', onContextMenu);
      window.removeEventListener('resize', onScrollOrResize);
      window.removeEventListener('scroll', onScrollOrResize, true);
    };
  }, [closeMenu, editor, scheduleUpdate]);

  if (!menu.open) return null;

  return createPortal(
    <div
      className="lexical-selection-menu"
      data-placement={menu.placement}
      style={{ left: menu.left, top: menu.top }}
      role="toolbar"
      aria-label="Text formatting"
      onMouseDown={(event) => event.preventDefault()}
    >
      {SELECTION_MENU_ACTIONS.map((action) =>
        action.kind === 'divider' ? (
          <span key={action.id} className="lexical-selection-menu-divider" />
        ) : (
          <SelectionMenuButton
            key={action.id}
            editor={editor}
            action={action}
            onDone={scheduleUpdate}
          />
        ),
      )}
    </div>,
    document.body,
  );
}

function SelectionMenuButton({
  editor,
  action,
  onDone,
}: {
  editor: LexicalEditor;
  action: MenuButtonAction;
  onDone: () => void;
}) {
  return (
    <button
      type="button"
      className="lexical-selection-menu-button"
      aria-label={action.label}
      title={action.label}
      onMouseDown={(event) => {
        event.preventDefault();
        runSelectionAction(editor, action);
        editor.focus();
        onDone();
      }}
    >
      {action.icon}
    </button>
  );
}

function runSelectionAction(editor: LexicalEditor, action: MenuButtonAction) {
  if (action.kind === 'format') {
    editor.dispatchCommand(FORMAT_TEXT_COMMAND, action.id as TextFormatType);
    return;
  }
  if (action.kind === 'link') {
    const url = window.prompt('Paste link');
    if (url == null) return;
    const trimmed = url.trim();
    if (!trimmed) return;
    editor.dispatchCommand(TOGGLE_LINK_COMMAND, {
      url: formatUrl(trimmed),
      target: '_blank',
      rel: 'noreferrer',
    });
    return;
  }
  if (action.kind === 'unlink') {
    editor.dispatchCommand(TOGGLE_LINK_COMMAND, null);
    return;
  }
  if (action.kind === 'clear') {
    clearSelectionFormatting(editor);
    return;
  }
  if (action.kind === 'case') {
    transformSelectionText(editor, action.id);
  }
}

function clearSelectionFormatting(editor: LexicalEditor) {
  editor.update(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) return;
    selection.setFormat(0);
    selection.setStyle('');
  });
}

function transformSelectionText(
  editor: LexicalEditor,
  transform: 'uppercase' | 'lowercase' | 'titlecase',
) {
  editor.update(() => {
    const selection = $getSelection();
    if (!$isRangeSelection(selection)) return;
    const text = selection.getTextContent();
    if (!text) return;
    selection.insertText(transformTextCase(text, transform));
  });
}

function transformTextCase(
  text: string,
  transform: 'uppercase' | 'lowercase' | 'titlecase',
) {
  if (transform === 'uppercase') return text.toLocaleUpperCase();
  if (transform === 'lowercase') return text.toLocaleLowerCase();
  return text
    .toLocaleLowerCase()
    .replace(/(^|[\s([{/"'`-])(\p{L})/gu, (_match, prefix: string, letter: string) =>
      `${prefix}${letter.toLocaleUpperCase()}`,
    );
}

function isUsableSelection(
  selection: Selection | null,
  rootElement: HTMLElement,
): selection is Selection {
  if (!selection || selection.rangeCount === 0 || selection.isCollapsed) {
    return false;
  }
  const anchorNode = selection.anchorNode;
  const focusNode = selection.focusNode;
  if (!anchorNode || !focusNode) return false;
  return rootElement.contains(anchorNode) && rootElement.contains(focusNode);
}

function getSelectionRect(selection: Selection) {
  const range = selection.getRangeAt(0);
  const rect = range.getBoundingClientRect();
  if (rect.width > 0 || rect.height > 0) return rect;

  for (const clientRect of Array.from(range.getClientRects())) {
    if (clientRect.width > 0 || clientRect.height > 0) {
      return clientRect;
    }
  }
  return null;
}

function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}
