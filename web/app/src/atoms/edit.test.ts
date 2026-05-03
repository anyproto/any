import { createStore } from 'jotai';
import { describe, expect, it } from 'vitest';
import {
  clearObjectTitleDraftsAtom,
  objectTitleDraftKey,
  objectTitleDraftsAtom,
  setObjectTitleDraftAtom,
} from './edit';

describe('object title drafts', () => {
  it('clears deleted object drafts in batches', () => {
    const store = createStore();
    store.set(setObjectTitleDraftAtom, {
      spaceId: 'spc-a',
      objectId: 'obj-1',
      title: 'One',
    });
    store.set(setObjectTitleDraftAtom, {
      spaceId: 'spc-a',
      objectId: 'obj-2',
      title: 'Two',
    });

    store.set(clearObjectTitleDraftsAtom, {
      spaceId: 'spc-a',
      objectIds: ['obj-1'],
    });

    expect(store.get(objectTitleDraftsAtom)).toEqual({
      [objectTitleDraftKey('spc-a', 'obj-2')]: 'Two',
    });
  });

  it('bounds stale draft storage', () => {
    const store = createStore();
    for (let index = 0; index < 105; index += 1) {
      store.set(setObjectTitleDraftAtom, {
        spaceId: 'spc-a',
        objectId: `obj-${index}`,
        title: `Draft ${index}`,
      });
    }

    const drafts = store.get(objectTitleDraftsAtom);
    expect(Object.keys(drafts)).toHaveLength(100);
    expect(drafts[objectTitleDraftKey('spc-a', 'obj-0')]).toBeUndefined();
    expect(drafts[objectTitleDraftKey('spc-a', 'obj-104')]).toBe('Draft 104');
  });
});
