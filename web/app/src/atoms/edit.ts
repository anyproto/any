import { atom } from 'jotai';

/**
 * The id of the object currently being renamed inline. Only one row
 * is in edit mode at a time. Set by ObjectRow's rename trigger;
 * cleared when the input commits or cancels.
 */
export const renamingObjectIdAtom = atom<string | null>(null);
