import { describe, expect, it } from 'vitest';
import { $getRoot, createEditor } from 'lexical';
import { CodeHighlightNode, CodeNode } from '@lexical/code';
import { AutoLinkNode, LinkNode } from '@lexical/link';
import {
  $convertFromMarkdownString,
  $convertToMarkdownString,
  CHECK_LIST,
} from '@lexical/markdown';
import {
  $createListItemNode,
  $createListNode,
  $isListItemNode,
  $isListNode,
  ListItemNode,
  ListNode,
} from '@lexical/list';
import { HeadingNode, QuoteNode } from '@lexical/rich-text';
import {
  CHECK_LIST_MARKDOWN_TRANSFORMER,
  MARKDOWN_TRANSFORMERS,
} from './lexicalMarkdownTransformers';

const markdownNodes = [
  HeadingNode,
  QuoteNode,
  ListNode,
  ListItemNode,
  CodeNode,
  CodeHighlightNode,
  LinkNode,
  AutoLinkNode,
];

function roundTrip(markdown: string) {
  const editor = createEditor({
    namespace: 'lexical-markdown-transformers-test',
    nodes: markdownNodes,
    onError(error) {
      throw error;
    },
  });
  let result = '';

  editor.update(
    () => {
      $convertFromMarkdownString(markdown, MARKDOWN_TRANSFORMERS);
      result = $convertToMarkdownString(MARKDOWN_TRANSFORMERS);
    },
    { discrete: true },
  );

  return result;
}

function inspectImportedFirstList(markdown: string) {
  const editor = createEditor({
    namespace: 'lexical-markdown-import-test',
    nodes: markdownNodes,
    onError(error) {
      throw error;
    },
  });
  let result: { type: string | null; checked: boolean | undefined } = {
    type: null,
    checked: undefined,
  };

  editor.update(
    () => {
      $convertFromMarkdownString(markdown, MARKDOWN_TRANSFORMERS);
      const first = $getRoot().getFirstChild();
      if (!$isListNode(first)) return;
      const item = first.getFirstChild();
      result = {
        type: first.getListType(),
        checked: $isListItemNode(item) ? item.getChecked() : undefined,
      };
    },
    { discrete: true },
  );

  return result;
}

function exportEditorState(build: () => void) {
  const editor = createEditor({
    namespace: 'lexical-markdown-export-test',
    nodes: markdownNodes,
    onError(error) {
      throw error;
    },
  });
  let result = '';

  editor.update(
    () => {
      build();
      result = $convertToMarkdownString(MARKDOWN_TRANSFORMERS);
    },
    { discrete: true },
  );

  return result;
}

describe('MARKDOWN_TRANSFORMERS', () => {
  it('checks checklist markdown before unordered list markdown', () => {
    expect(MARKDOWN_TRANSFORMERS[0]).not.toBe(CHECK_LIST);
    expect(MARKDOWN_TRANSFORMERS[0]).toBe(CHECK_LIST_MARKDOWN_TRANSFORMER);
    expect('- [ ]'.match(CHECK_LIST_MARKDOWN_TRANSFORMER.regExp)).toBeTruthy();
  });

  it('round-trips checked and unchecked list items', () => {
    expect(roundTrip('- [x] Done\n- [ ] Later')).toBe('- [x] Done\n- [ ] Later');
  });

  it('round-trips an empty checkbox block', () => {
    expect(inspectImportedFirstList('- [ ]')).toEqual({
      type: 'check',
      checked: false,
    });
    expect(roundTrip('- [ ]')).toBe('- [ ]');
    expect(
      exportEditorState(() => {
        const list = $createListNode('check');
        list.append($createListItemNode(false));
        $getRoot().append(list);
      }).trimEnd(),
    ).toBe('- [ ]');
  });

  it('keeps unordered and ordered list syntax intact', () => {
    expect(roundTrip('- Alpha\n- Beta\n\n1. One\n2. Two')).toBe(
      '- Alpha\n- Beta\n\n1. One\n2. Two',
    );
  });
});
