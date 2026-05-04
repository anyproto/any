import { atom } from 'jotai';
import { atomWithStorage } from 'jotai/utils';
import { appStorage } from '@/shared';

export const settingsOpenAtom = atom(false);

export type EditorEngine = 'blocknote' | 'lexical';

export const editorEngineAtom = atomWithStorage<EditorEngine>(
  'any.editor.engine',
  'lexical',
  appStorage<EditorEngine>(),
  { getOnInit: true },
);
