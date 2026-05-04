import { afterEach, describe, expect, it } from 'vitest';
import { useState } from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { LexicalComposer } from '@lexical/react/LexicalComposer';
import { RichTextPlugin } from '@lexical/react/LexicalRichTextPlugin';
import { ContentEditable } from '@lexical/react/LexicalContentEditable';
import LexicalErrorBoundary from '@lexical/react/LexicalErrorBoundary';
import { ListPlugin } from '@lexical/react/LexicalListPlugin';
import {
  $createListItemNode,
  $createListNode,
  ListItemNode,
  ListNode,
} from '@lexical/list';
import { $createTextNode, $getRoot } from 'lexical';
import { LexicalBlockMenu } from './LexicalBlockMenu';

let dragEventDescriptor: PropertyDescriptor | undefined;
let patchedDragEvent = false;
let elementFromPointDescriptor: PropertyDescriptor | undefined;
let patchedElementFromPoint = false;

describe('<LexicalBlockMenu>', () => {
  afterEach(() => {
    if (patchedDragEvent) {
      if (dragEventDescriptor) {
        Object.defineProperty(globalThis, 'DragEvent', dragEventDescriptor);
      } else {
        Reflect.deleteProperty(globalThis, 'DragEvent');
      }
    }
    dragEventDescriptor = undefined;
    patchedDragEvent = false;
    if (patchedElementFromPoint) {
      if (elementFromPointDescriptor) {
        Object.defineProperty(document, 'elementFromPoint', elementFromPointDescriptor);
      } else {
        Reflect.deleteProperty(document, 'elementFromPoint');
      }
    }
    elementFromPointDescriptor = undefined;
    patchedElementFromPoint = false;
    cleanup();
    document.body.innerHTML = '';
  });

  it('renders the insert and drag controls into the editor anchor', () => {
    const anchor = document.createElement('div');
    const editable = document.createElement('div');
    editable.className = 'ContentEditable__root';
    anchor.appendChild(editable);
    document.body.appendChild(anchor);

    render(
      <LexicalComposer
        initialConfig={{
          namespace: 'block-menu-test',
          nodes: [],
          onError(error: Error) {
            throw error;
          },
        }}
      >
        <LexicalBlockMenu anchorElem={anchor} />
      </LexicalComposer>,
    );

    expect(within(anchor).getByLabelText('Add block below')).toBeTruthy();
    expect(anchor.querySelector('.lexical-block-menu-handle')).toBeTruthy();
  });

  it('moves list items with the pointer-driven block handle', async () => {
    render(<TestListEditor namespace="block-menu-pointer-test" />);

    const one = await screen.findByText('one');
    const two = await screen.findByText('two');
    const three = await screen.findByText('three');
    mockListRects(one, two, three);
    mockElementFromPoint(() => two.closest('li')!);

    fireEvent.mouseMove(one.closest('li')!, { clientY: 24 });
    await waitFor(() => {
      expect(document.querySelector('.lexical-block-menu')?.getAttribute('aria-hidden')).toBe(
        'false',
      );
    });
    const dragHandle = screen.getByLabelText('Drag block');

    fireEvent(dragHandle, createPointerDomEvent('pointerdown', 24, 12));
    fireEvent(document, createPointerDomEvent('pointermove', 76, 20));
    fireEvent(document, createPointerDomEvent('pointerup', 76, 20));

    await waitFor(() => {
      const labels = screen.getAllByText(/one|two|three/).map((node) => node.textContent);
      expect(labels).toEqual(['two', 'one', 'three']);
    });
  });

  it('moves individual list items from the block drag handle', async () => {
    dragEventDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'DragEvent');
    patchedDragEvent = true;
    Object.defineProperty(globalThis, 'DragEvent', {
      configurable: true,
      value: Event,
    });

    render(<TestListEditor namespace="block-menu-drag-test" />);

    const one = await screen.findByText('one');
    const two = await screen.findByText('two');
    const three = await screen.findByText('three');
    mockListRects(one, two, three);

    fireEvent.mouseMove(one.closest('li')!, { clientY: 24 });
    await waitFor(() => {
      expect(document.querySelector('.lexical-block-menu')?.getAttribute('aria-hidden')).toBe(
        'false',
      );
    });
    const dragHandle = document.querySelector('.lexical-block-menu-handle')!;
    const dataTransfer = createDataTransfer();

    fireEvent.dragStart(dragHandle, { dataTransfer });
    expect(dataTransfer.getData('application/x-any-lexical-drag-block')).not.toBe('');
    fireEvent(two.closest('li')!, createDragDomEvent('dragover', 76, dataTransfer));
    fireEvent(two.closest('li')!, createDragDomEvent('drop', 76, dataTransfer));

    await waitFor(() => {
      const labels = screen.getAllByText(/one|two|three/).map((node) => node.textContent);
      expect(labels).toEqual(['two', 'one', 'three']);
    });
  });
});

function TestListEditor({ namespace }: { namespace: string }) {
  const [anchor, setAnchor] = useState<HTMLDivElement | null>(null);

  return (
    <LexicalComposer
      initialConfig={{
        namespace,
        nodes: [ListNode, ListItemNode],
        theme: {
          list: {
            listitem: 'lexical-list-item',
          },
        },
        editorState() {
          const root = $getRoot();
          const list = $createListNode('bullet');
          for (const label of ['one', 'two', 'three']) {
            const item = $createListItemNode();
            item.append($createTextNode(label));
            list.append(item);
          }
          root.append(list);
        },
        onError(error: Error) {
          throw error;
        },
      }}
    >
      <div ref={setAnchor}>
        <RichTextPlugin
          contentEditable={<ContentEditable className="ContentEditable__root" />}
          ErrorBoundary={LexicalErrorBoundary}
        />
        <ListPlugin />
        {anchor ? <LexicalBlockMenu anchorElem={anchor} /> : null}
      </div>
    </LexicalComposer>
  );
}

function mockListRects(one: Element, two: Element, three: Element) {
  mockRect(one.closest('li')!, { top: 10, bottom: 40 });
  mockRect(two.closest('li')!, { top: 50, bottom: 80 });
  mockRect(three.closest('li')!, { top: 90, bottom: 120 });
}

function mockElementFromPoint(resolveElement: () => Element) {
  elementFromPointDescriptor = Object.getOwnPropertyDescriptor(
    document,
    'elementFromPoint',
  );
  patchedElementFromPoint = true;
  Object.defineProperty(document, 'elementFromPoint', {
    configurable: true,
    value: resolveElement,
  });
}

function mockRect(element: Element, rect: { top: number; bottom: number }) {
  Object.defineProperty(element, 'getBoundingClientRect', {
    configurable: true,
    value: () =>
      ({
        bottom: rect.bottom,
        height: rect.bottom - rect.top,
        left: 0,
        right: 300,
        top: rect.top,
        width: 300,
        x: 0,
        y: rect.top,
        toJSON: () => ({}),
      }) as DOMRect,
  });
}

function createDataTransfer(): DataTransfer {
  const values = new Map<string, string>();
  return {
    clearData: (format?: string) => {
      if (format) values.delete(format);
      else values.clear();
    },
    dropEffect: 'move',
    effectAllowed: 'all',
    files: [] as unknown as FileList,
    getData: (format: string) => values.get(format) ?? '',
    items: [] as unknown as DataTransferItemList,
    setData: (format: string, data: string) => {
      values.set(format, data);
    },
    setDragImage: (image: Element, x: number, y: number) => {
      values.set('__dragImage', `${image.nodeName}:${x}:${y}`);
    },
    types: [],
  };
}

function createDragDomEvent(
  type: 'dragover' | 'drop',
  clientY: number,
  dataTransfer: DataTransfer,
) {
  const event = new Event(type, { bubbles: true, cancelable: true }) as DragEvent;
  Object.defineProperties(event, {
    clientY: { value: clientY },
    dataTransfer: { value: dataTransfer },
  });
  return event;
}

function createPointerDomEvent(
  type: 'pointerdown' | 'pointermove' | 'pointerup',
  clientY: number,
  clientX: number,
) {
  const event = new Event(type, { bubbles: true, cancelable: true }) as PointerEvent;
  Object.defineProperties(event, {
    button: { value: 0 },
    clientX: { value: clientX },
    clientY: { value: clientY },
    pointerId: { value: 1 },
  });
  return event;
}
