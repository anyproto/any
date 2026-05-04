import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type DragEvent,
  type MouseEvent,
  type PointerEvent as ReactPointerEvent,
} from 'react';
import { createPortal } from 'react-dom';
import { useLexicalComposerContext } from '@lexical/react/LexicalComposerContext';
import { GripVertical, Plus } from 'lucide-react';
import {
  $createListItemNode,
  $createListNode,
  $isListItemNode,
  $isListNode,
} from '@lexical/list';
import {
  $createParagraphNode,
  $getNearestNodeFromDOMNode,
  $getNodeByKey,
  $getRoot,
  $isRootOrShadowRoot,
  COMMAND_PRIORITY_HIGH,
  COMMAND_PRIORITY_LOW,
  DRAGOVER_COMMAND,
  DROP_COMMAND,
  type LexicalEditor,
  type LexicalNode,
} from 'lexical';
import { mergeRegister } from '@lexical/utils';

interface Props {
  anchorElem: HTMLElement;
}

const DRAG_DATA_FORMAT = 'application/x-any-lexical-drag-block';
const LIST_ITEM_SELECTOR = 'li.lexical-list-item, li.lexical-check-list-item';

interface PointerDragState {
  clientY: number;
  nodeKey: string;
  pointerId: number;
  targetElement: HTMLElement | null;
}

export function LexicalBlockMenu({ anchorElem }: Props) {
  const [editor] = useLexicalComposerContext();
  const menuRef = useRef<HTMLDivElement | null>(null);
  const targetLineRef = useRef<HTMLDivElement | null>(null);
  const activeElementRef = useRef<HTMLElement | null>(null);
  const isDraggingBlockRef = useRef(false);
  const draggedNodeKeyRef = useRef<string | null>(null);
  const pointerDragRef = useRef<PointerDragState | null>(null);
  const [activeElement, setActiveElement] = useState<HTMLElement | null>(null);

  const setActiveBlockElement = useCallback(
    (element: HTMLElement | null) => {
      activeElementRef.current = element;
      setActiveElement(element);
      setMenuPosition(element, menuRef.current, anchorElem);
    },
    [anchorElem],
  );

  const handleAddBlock = useCallback(
    (event: MouseEvent<HTMLButtonElement>) => {
      event.preventDefault();
      event.stopPropagation();
      if (activeElement == null) return;

      editor.update(() => {
        const activeNode = $getNearestNodeFromDOMNode(activeElement);
        if (activeNode == null || $isRootOrShadowRoot(activeNode)) return;

        if ($isListItemNode(activeNode)) {
          const item = $createListItemNode(
            activeNode.getChecked() === undefined ? undefined : false,
          );
          activeNode.insertAfter(item);
          item.selectStart();
          return;
        }

        const paragraph = $createParagraphNode();
        activeNode.insertAfter(paragraph);
        paragraph.selectStart();
      });
    },
    [activeElement, editor],
  );

  const readNodeKeyFromElement = useCallback(
    (element: HTMLElement | null) => {
      if (element == null) return '';

      let nodeKey = '';
      editor.getEditorState().read(
        () => {
          nodeKey = $getNearestNodeFromDOMNode(element)?.getKey() ?? '';
        },
        { editor },
      );
      return nodeKey;
    },
    [editor],
  );

  const moveDraggedBlockInUpdate = useCallback(
    (
      draggedKey: string | null | undefined,
      targetBlockElem: HTMLElement | null,
      clientY: number,
    ) => {
      const resolvedDraggedKey = draggedKey ?? draggedNodeKeyRef.current;
      if (!resolvedDraggedKey || targetBlockElem == null) return false;

      const draggedNode = $getNodeByKey(resolvedDraggedKey);
      const targetNode = $getNearestNodeFromDOMNode(targetBlockElem);
      if (draggedNode == null || targetNode == null || draggedNode === targetNode) {
        return false;
      }

      moveNodeNearTarget({
        draggedNode,
        placeAfter: isAfterTarget(targetBlockElem, clientY),
        targetNode,
      });
      return true;
    },
    [],
  );

  const resetDragState = useCallback(() => {
    isDraggingBlockRef.current = false;
    draggedNodeKeyRef.current = null;
    pointerDragRef.current = null;
    hideTargetLine(targetLineRef.current);
  }, []);

  const isOnMenu = useCallback((element: HTMLElement) => {
    return menuRef.current?.contains(element) ?? false;
  }, []);

  useEffect(() => {
    const scrollerElem = anchorElem.parentElement ?? anchorElem;

    const onMouseMove = (event: globalThis.MouseEvent) => {
      const target = event.target;
      if (!(target instanceof HTMLElement)) {
        setActiveBlockElement(null);
        return;
      }
      if (isOnMenu(target)) return;

      setActiveBlockElement(getBlockElement(anchorElem, editor, event.clientY, target));
    };

    const onMouseLeave = (event: globalThis.MouseEvent) => {
      const relatedTarget = event.relatedTarget;
      if (relatedTarget instanceof HTMLElement && isOnMenu(relatedTarget)) return;
      setActiveBlockElement(null);
    };

    scrollerElem.addEventListener('mousemove', onMouseMove);
    scrollerElem.addEventListener('mouseleave', onMouseLeave);
    return () => {
      scrollerElem.removeEventListener('mousemove', onMouseMove);
      scrollerElem.removeEventListener('mouseleave', onMouseLeave);
    };
  }, [anchorElem, editor, isOnMenu, setActiveBlockElement]);

  useEffect(() => {
    const onNativeDragOver = (event: globalThis.DragEvent) => {
      if (!isDraggingBlockRef.current) return;

      const targetBlockElem = getBlockElement(
        anchorElem,
        editor,
        event.clientY,
        event.target,
        true,
      );
      const targetLineElem = targetLineRef.current;
      if (targetBlockElem == null || targetLineElem == null) return;

      setTargetLine(targetLineElem, targetBlockElem, event.clientY, anchorElem);
      event.preventDefault();
      if (event.dataTransfer != null) event.dataTransfer.dropEffect = 'move';
    };

    const onNativeDrop = (event: globalThis.DragEvent) => {
      if (!isDraggingBlockRef.current) return;

      const targetBlockElem = getBlockElement(
        anchorElem,
        editor,
        event.clientY,
        event.target,
        true,
      );
      let didMove = false;
      editor.update(() => {
        didMove = moveDraggedBlockInUpdate(
          event.dataTransfer?.getData(DRAG_DATA_FORMAT),
          targetBlockElem,
          event.clientY,
        );
      });
      resetDragState();
      setActiveBlockElement(null);
      if (!didMove) return;

      event.preventDefault();
      event.stopPropagation();
    };

    anchorElem.addEventListener('dragover', onNativeDragOver, true);
    anchorElem.addEventListener('drop', onNativeDrop, true);

    return mergeRegister(
      editor.registerCommand(
        DRAGOVER_COMMAND,
        (event) => {
          if (!isDraggingBlockRef.current) return false;
          const targetBlockElem = getBlockElement(
            anchorElem,
            editor,
            event.clientY,
            event.target,
            true,
          );
          const targetLineElem = targetLineRef.current;
          if (targetBlockElem == null || targetLineElem == null) return false;

          setTargetLine(targetLineElem, targetBlockElem, event.clientY, anchorElem);
          event.preventDefault();
          return true;
        },
        COMMAND_PRIORITY_LOW,
      ),
      editor.registerCommand(
        DROP_COMMAND,
        (event) => {
          if (!isDraggingBlockRef.current) return false;

          const targetBlockElem = getBlockElement(
            anchorElem,
            editor,
            event.clientY,
            event.target,
            true,
          );
          if (targetBlockElem == null) return false;

          const didMove = moveDraggedBlockInUpdate(
            event.dataTransfer?.getData(DRAG_DATA_FORMAT),
            targetBlockElem,
            event.clientY,
          );
          if (!didMove) return false;

          event.preventDefault();
          resetDragState();
          setActiveBlockElement(null);
          return true;
        },
        COMMAND_PRIORITY_HIGH,
      ),
      () => {
        anchorElem.removeEventListener('dragover', onNativeDragOver, true);
        anchorElem.removeEventListener('drop', onNativeDrop, true);
      },
    );
  }, [anchorElem, editor, moveDraggedBlockInUpdate, resetDragState, setActiveBlockElement]);

  useEffect(() => {
    const onPointerMove = (event: globalThis.PointerEvent) => {
      const pointerDrag = pointerDragRef.current;
      if (pointerDrag == null || pointerDrag.pointerId !== event.pointerId) return;

      const eventTarget = document.elementFromPoint(event.clientX, event.clientY);
      const targetBlockElem = getBlockElement(
        anchorElem,
        editor,
        event.clientY,
        eventTarget,
        true,
      );
      pointerDrag.targetElement = targetBlockElem;
      pointerDrag.clientY = event.clientY;

      const targetLineElem = targetLineRef.current;
      if (targetBlockElem != null && targetLineElem != null) {
        setTargetLine(targetLineElem, targetBlockElem, event.clientY, anchorElem);
      }
      event.preventDefault();
    };

    const finishPointerDrag = (event: globalThis.PointerEvent) => {
      const pointerDrag = pointerDragRef.current;
      if (pointerDrag == null || pointerDrag.pointerId !== event.pointerId) return;

      const eventTarget = document.elementFromPoint(event.clientX, event.clientY);
      const targetBlockElem =
        pointerDrag.targetElement ??
        getBlockElement(anchorElem, editor, event.clientY, eventTarget, true);
      let didMove = false;
      editor.update(() => {
        didMove = moveDraggedBlockInUpdate(
          pointerDrag.nodeKey,
          targetBlockElem,
          event.clientY,
        );
      });

      resetDragState();
      setActiveBlockElement(null);
      if (!didMove) return;

      event.preventDefault();
      event.stopPropagation();
    };

    document.addEventListener('pointermove', onPointerMove, true);
    document.addEventListener('pointerup', finishPointerDrag, true);
    document.addEventListener('pointercancel', finishPointerDrag, true);
    return () => {
      document.removeEventListener('pointermove', onPointerMove, true);
      document.removeEventListener('pointerup', finishPointerDrag, true);
      document.removeEventListener('pointercancel', finishPointerDrag, true);
    };
  }, [anchorElem, editor, moveDraggedBlockInUpdate, resetDragState, setActiveBlockElement]);

  const handlePointerDown = useCallback(
    (event: ReactPointerEvent<HTMLButtonElement>) => {
      if (event.button !== 0) return;

      const activeElement = activeElementRef.current;
      const nodeKey = readNodeKeyFromElement(activeElement);
      if (!nodeKey) return;

      pointerDragRef.current = {
        clientY: event.clientY,
        nodeKey,
        pointerId: event.pointerId,
        targetElement: activeElement,
      };
      isDraggingBlockRef.current = true;
      draggedNodeKeyRef.current = nodeKey;
      event.preventDefault();
      event.stopPropagation();
    },
    [readNodeKeyFromElement],
  );

  const handleDragStart = useCallback(
    (event: DragEvent<HTMLButtonElement>) => {
      const activeElement = activeElementRef.current;
      const dataTransfer = event.dataTransfer;
      if (activeElement == null || dataTransfer == null) {
        event.preventDefault();
        return;
      }

      const nodeKey = readNodeKeyFromElement(activeElement);
      if (!nodeKey) {
        event.preventDefault();
        return;
      }

      isDraggingBlockRef.current = true;
      draggedNodeKeyRef.current = nodeKey;
      dataTransfer.effectAllowed = 'move';
      dataTransfer.setData(DRAG_DATA_FORMAT, nodeKey);
      dataTransfer.setDragImage(activeElement, 0, 0);
    },
    [readNodeKeyFromElement],
  );

  const handleDragEnd = useCallback(() => {
    resetDragState();
  }, [resetDragState]);

  return createPortal(
    <>
      <div
        ref={menuRef}
        className="lexical-block-menu"
        aria-hidden={activeElement == null}
      >
        <button
          type="button"
          className="lexical-block-menu-button"
          aria-label="Add block below"
          draggable={false}
          onClick={handleAddBlock}
          onDragStart={(event) => event.preventDefault()}
        >
          <Plus aria-hidden="true" focusable="false" size={18} strokeWidth={2} />
        </button>
        <button
          type="button"
          className="lexical-block-menu-handle"
          aria-label="Drag block"
          draggable
          onPointerDown={handlePointerDown}
          onDragStart={handleDragStart}
          onDragEnd={handleDragEnd}
        >
          <GripVertical
            aria-hidden="true"
            focusable="false"
            size={22}
            strokeWidth={2}
          />
        </button>
      </div>
      <div ref={targetLineRef} className="lexical-block-menu-target-line" />
    </>,
    anchorElem,
  );
}

function getBlockElement(
  anchorElem: HTMLElement,
  editor: LexicalEditor,
  clientY: number,
  eventTarget: EventTarget | null,
  useEdgeAsDefault = false,
): HTMLElement | null {
  if (eventTarget instanceof HTMLElement) {
    const directListItem = eventTarget.closest<HTMLElement>(LIST_ITEM_SELECTOR);
    if (directListItem != null && anchorElem.contains(directListItem)) {
      return directListItem;
    }
  }

  const listItemAtY = getListItemElementAtY(anchorElem, clientY);
  if (listItemAtY != null) return listItemAtY;

  return getTopLevelElementAtY(anchorElem, editor, clientY, useEdgeAsDefault);
}

function getListItemElementAtY(
  anchorElem: HTMLElement,
  clientY: number,
): HTMLElement | null {
  for (const element of anchorElem.querySelectorAll<HTMLElement>(LIST_ITEM_SELECTOR)) {
    const rect = element.getBoundingClientRect();
    if (clientY >= rect.top - 3 && clientY <= rect.bottom + 3) {
      return element;
    }
  }
  return null;
}

function getTopLevelElementAtY(
  anchorElem: HTMLElement,
  editor: LexicalEditor,
  clientY: number,
  useEdgeAsDefault: boolean,
): HTMLElement | null {
  const candidates: HTMLElement[] = [];
  editor.getEditorState().read(() => {
    for (const key of $getRoot().getChildrenKeys()) {
      const element = editor.getElementByKey(key);
      if (element != null) candidates.push(element);
    }
  });

  if (candidates.length === 0) return null;
  const first = candidates[0];
  const last = candidates[candidates.length - 1];
  if (useEdgeAsDefault && first && clientY < first.getBoundingClientRect().top) {
    return first;
  }
  if (useEdgeAsDefault && last && clientY > last.getBoundingClientRect().bottom) {
    return last;
  }

  const anchorRect = anchorElem.getBoundingClientRect();
  for (const element of candidates) {
    const rect = element.getBoundingClientRect();
    const styles = window.getComputedStyle(element);
    const top = rect.top - Number.parseFloat(styles.marginTop || '0');
    const bottom = rect.bottom + Number.parseFloat(styles.marginBottom || '0');
    if (
      clientY >= top &&
      clientY <= bottom &&
      rect.right >= anchorRect.left &&
      rect.left <= anchorRect.right
    ) {
      return element;
    }
  }

  return null;
}

function setMenuPosition(
  targetElem: HTMLElement | null,
  menuElem: HTMLElement | null,
  anchorElem: HTMLElement,
) {
  if (menuElem == null) return;
  if (targetElem == null) {
    menuElem.style.display = 'none';
    menuElem.style.opacity = '0';
    return;
  }

  const targetRect = targetElem.getBoundingClientRect();
  const anchorRect = anchorElem.getBoundingClientRect();
  const menuRect = menuElem.getBoundingClientRect();
  const styles = window.getComputedStyle(targetElem);
  const lineHeight = Number.parseFloat(styles.lineHeight || '');
  const targetHeight = Number.isFinite(lineHeight) ? lineHeight : targetRect.height;
  const top =
    targetRect.top +
    (targetHeight - (menuRect.height || targetHeight)) / 2 -
    anchorRect.top +
    anchorElem.scrollTop;

  menuElem.style.display = 'flex';
  menuElem.style.opacity = '1';
  menuElem.style.transform = `translate(0px, ${top}px)`;
}

function setTargetLine(
  targetLineElem: HTMLElement,
  targetElem: HTMLElement,
  clientY: number,
  anchorElem: HTMLElement,
) {
  const targetRect = targetElem.getBoundingClientRect();
  const anchorRect = anchorElem.getBoundingClientRect();
  const top =
    (isAfterTarget(targetElem, clientY) ? targetRect.bottom : targetRect.top) -
    anchorRect.top -
    1 +
    anchorElem.scrollTop;

  targetLineElem.style.opacity = '0.45';
  targetLineElem.style.width = `${anchorRect.width - 32}px`;
  targetLineElem.style.transform = `translate(16px, ${top}px)`;
}

function hideTargetLine(targetLineElem: HTMLElement | null) {
  if (targetLineElem == null) return;
  targetLineElem.style.opacity = '0';
  targetLineElem.style.transform = 'translate(-10000px, -10000px)';
}

function isAfterTarget(targetElem: HTMLElement, clientY: number) {
  const rect = targetElem.getBoundingClientRect();
  return clientY >= rect.top + rect.height / 2;
}

function moveNodeNearTarget({
  draggedNode,
  targetNode,
  placeAfter,
}: {
  draggedNode: LexicalNode;
  targetNode: LexicalNode;
  placeAfter: boolean;
}) {
  if ($isListItemNode(draggedNode)) {
    if ($isListItemNode(targetNode)) {
      if (placeAfter) {
        targetNode.insertAfter(draggedNode);
      } else {
        targetNode.insertBefore(draggedNode);
      }
      return;
    }

    const oldParent = draggedNode.getParent();
    const listType = $isListNode(oldParent) ? oldParent.getListType() : 'bullet';
    const wrapper = $createListNode(listType);
    wrapper.append(draggedNode);
    if (placeAfter) {
      targetNode.insertAfter(wrapper);
    } else {
      targetNode.insertBefore(wrapper);
    }
    if ($isListNode(oldParent) && oldParent.getChildrenSize() === 0) {
      oldParent.remove();
    }
    return;
  }

  if ($isListItemNode(targetNode)) {
    const listParent = targetNode.getParent();
    if (listParent != null) {
      if (placeAfter) {
        listParent.insertAfter(draggedNode);
      } else {
        listParent.insertBefore(draggedNode);
      }
      return;
    }
  }

  if (placeAfter) {
    targetNode.insertAfter(draggedNode);
  } else {
    targetNode.insertBefore(draggedNode);
  }
}
