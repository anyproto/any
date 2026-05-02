import { ApiError } from '@/lib/api/client';

/**
 * State machine for the Create Type wizard.
 *
 * Per docs/08-app-architecture.md, this is the second hand-rolled
 * machine in the codebase (after the editor save machine). If a third
 * one lands we adopt XState.
 */

export type PropertyKindUI = 'string' | 'number' | 'boolean';

export interface PropertyDraft {
  /** Stable client-only id for list keys. Not sent to server. */
  rowId: string;
  name: string;
  kind: PropertyKindUI;
}

export type WizardState =
  | { kind: 'name'; name: string; description: string }
  | {
      kind: 'properties';
      name: string;
      description: string;
      properties: PropertyDraft[];
    }
  | {
      kind: 'confirm';
      name: string;
      description: string;
      properties: PropertyDraft[];
    }
  | {
      kind: 'creating';
      name: string;
      description: string;
      properties: PropertyDraft[];
      progress: number; // # of operations completed (0..total)
      total: number; // 1 (createType) + properties.length
    }
  | { kind: 'done'; typeId: string }
  | {
      kind: 'create_error';
      name: string;
      description: string;
      properties: PropertyDraft[];
      error: ApiError;
      partial: { typeId: string | null; createdProps: number };
    };

export type WizardAction =
  | { type: 'edit_name'; name: string }
  | { type: 'edit_description'; description: string }
  | { type: 'next_to_properties' }
  | { type: 'add_property' }
  | { type: 'remove_property'; rowId: string }
  | { type: 'edit_property_name'; rowId: string; name: string }
  | { type: 'edit_property_kind'; rowId: string; kind: PropertyKindUI }
  | { type: 'next_to_confirm' }
  | { type: 'back' }
  | { type: 'start_create' }
  | { type: 'progress'; completed: number; typeId?: string }
  | { type: 'create_ok'; typeId: string }
  | {
      type: 'create_failed';
      error: ApiError;
      partial: { typeId: string | null; createdProps: number };
    }
  | { type: 'reset' };

export const initial: WizardState = { kind: 'name', name: '', description: '' };

const MAX_PROPERTIES = 10;

let _rowSeq = 0;
function nextRowId(): string {
  _rowSeq += 1;
  return `row-${_rowSeq}`;
}

export function reduce(state: WizardState, action: WizardAction): WizardState {
  switch (action.type) {
    case 'reset':
      return initial;

    case 'edit_name':
      if (state.kind === 'name') return { ...state, name: action.name };
      return state;

    case 'edit_description':
      if (state.kind === 'name') return { ...state, description: action.description };
      return state;

    case 'next_to_properties':
      if (state.kind !== 'name') return state;
      if (state.name.trim() === '') return state;
      return {
        kind: 'properties',
        name: state.name,
        description: state.description,
        properties: [],
      };

    case 'add_property':
      if (state.kind !== 'properties') return state;
      if (state.properties.length >= MAX_PROPERTIES) return state;
      return {
        ...state,
        properties: [...state.properties, { rowId: nextRowId(), name: '', kind: 'string' }],
      };

    case 'remove_property':
      if (state.kind !== 'properties') return state;
      return {
        ...state,
        properties: state.properties.filter((p) => p.rowId !== action.rowId),
      };

    case 'edit_property_name':
      if (state.kind !== 'properties') return state;
      return {
        ...state,
        properties: state.properties.map((p) =>
          p.rowId === action.rowId ? { ...p, name: action.name } : p,
        ),
      };

    case 'edit_property_kind':
      if (state.kind !== 'properties') return state;
      return {
        ...state,
        properties: state.properties.map((p) =>
          p.rowId === action.rowId ? { ...p, kind: action.kind } : p,
        ),
      };

    case 'next_to_confirm':
      if (state.kind !== 'properties') return state;
      // Block if any property has a blank name.
      if (state.properties.some((p) => p.name.trim() === '')) return state;
      return {
        kind: 'confirm',
        name: state.name,
        description: state.description,
        properties: state.properties,
      };

    case 'back':
      if (state.kind === 'properties') {
        return { kind: 'name', name: state.name, description: state.description };
      }
      if (state.kind === 'confirm') {
        return {
          kind: 'properties',
          name: state.name,
          description: state.description,
          properties: state.properties,
        };
      }
      if (state.kind === 'create_error') {
        return {
          kind: 'confirm',
          name: state.name,
          description: state.description,
          properties: state.properties,
        };
      }
      return state;

    case 'start_create':
      if (state.kind !== 'confirm' && state.kind !== 'create_error') return state;
      return {
        kind: 'creating',
        name: state.name,
        description: state.description,
        properties: state.properties,
        progress: 0,
        total: 1 + state.properties.length,
      };

    case 'progress':
      if (state.kind !== 'creating') return state;
      return { ...state, progress: action.completed };

    case 'create_ok':
      return { kind: 'done', typeId: action.typeId };

    case 'create_failed':
      if (state.kind !== 'creating') return state;
      return {
        kind: 'create_error',
        name: state.name,
        description: state.description,
        properties: state.properties,
        error: action.error,
        partial: action.partial,
      };

    default:
      return state;
  }
}

export function canGoNext(state: WizardState): boolean {
  switch (state.kind) {
    case 'name':
      return state.name.trim().length > 0;
    case 'properties':
      return state.properties.every((p) => p.name.trim().length > 0);
    default:
      return false;
  }
}

export const wizardLimits = { MAX_PROPERTIES };
