import {
  CHECK_LIST,
  TRANSFORMERS,
  type ElementTransformer,
  type Transformer,
} from '@lexical/markdown';

// Lexical's built-in CHECK_LIST regexp requires whitespace after "[ ]"/"[x]",
// so an empty persisted checkbox line like "- [ ]" falls through to bullets.
export const CHECK_LIST_MARKDOWN_TRANSFORMER: ElementTransformer = {
  ...CHECK_LIST,
  export: (node, traverseChildren) => {
    const markdown = CHECK_LIST.export(node, traverseChildren);
    return markdown?.replace(/^(\s*[-*+]\s\[[ x]\])\s+$/gim, '$1') ?? null;
  },
  regExp: /^(\s*)(?:[-*+]\s)?\s?(\[(\s|x)?\])(?:\s|$)/i,
};

// Lexical's default TRANSFORMERS omit checklists. Keep our checklist transformer
// first so "- [ ]" imports as a checklist instead of a bullet with literal text.
export const MARKDOWN_TRANSFORMERS: Transformer[] = [
  CHECK_LIST_MARKDOWN_TRANSFORMER,
  ...TRANSFORMERS.filter((transformer) => transformer !== CHECK_LIST),
];
