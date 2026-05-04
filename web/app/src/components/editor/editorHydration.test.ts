import { describe, expect, it } from 'vitest';
import { shouldHydrateMarkdownEditor } from './editorHydration';

describe('shouldHydrateMarkdownEditor', () => {
  it('waits until markdown is loaded', () => {
    expect(
      shouldHydrateMarkdownEditor({
        hydratedIdentity: null,
        identityKey: 'space-a/object-a',
        markdown: null,
      }),
    ).toBe(false);
  });

  it('hydrates when opening a new object identity', () => {
    expect(
      shouldHydrateMarkdownEditor({
        hydratedIdentity: 'space-a/object-a',
        identityKey: 'space-a/object-b',
        markdown: '# Object B',
      }),
    ).toBe(true);
  });

  it('does not hydrate same-object autosave cache refreshes', () => {
    expect(
      shouldHydrateMarkdownEditor({
        hydratedIdentity: 'space-a/object-a',
        identityKey: 'space-a/object-a',
        markdown: '# Object A after save',
      }),
    ).toBe(false);
  });
});
