import { ApiError } from '@/lib/api/client';

/**
 * State machine for the markdown editor's save flow.
 *
 * This is the first hand-rolled machine in the frontend: a
 * discriminated union driven by useReducer. Per docs/agents/README.md,
 * if a third independent machine lands, adopt a proper state-machine
 * library instead of scaling bespoke reducers.
 */
export type SaveState =
  | { kind: 'loading' }
  | { kind: 'load_error'; error: ApiError }
  | { kind: 'idle'; savedContent: string }
  | {
      kind: 'dirty';
      savedContent: string;
      nextContent: string;
      since: number;
    }
  | {
      kind: 'saving';
      savedContent: string;
      inflightContent: string;
    }
  | {
      kind: 'save_error';
      savedContent: string;
      nextContent: string;
      error: ApiError;
    };

export type SaveAction =
  | { type: 'load_ok'; content: string }
  | { type: 'load_failed'; error: ApiError }
  | { type: 'edit'; content: string; now: number }
  | { type: 'flush' }
  | { type: 'save_ok' }
  | { type: 'save_failed'; error: ApiError }
  | { type: 'reload' }; // user switched object — back to loading

export const initial: SaveState = { kind: 'loading' };

export function reduce(state: SaveState, action: SaveAction): SaveState {
  switch (action.type) {
    case 'reload':
      return { kind: 'loading' };

    case 'load_ok':
      return { kind: 'idle', savedContent: action.content };

    case 'load_failed':
      return { kind: 'load_error', error: action.error };

    case 'edit': {
      // Edits in any non-loading state set the doc dirty.
      if (state.kind === 'loading' || state.kind === 'load_error') return state;
      const savedContent =
        'savedContent' in state ? state.savedContent : '';
      // No-op edit (same as saved) → back to idle.
      if (action.content === savedContent) {
        return { kind: 'idle', savedContent };
      }
      return {
        kind: 'dirty',
        savedContent,
        nextContent: action.content,
        since: action.now,
      };
    }

    case 'flush': {
      if (state.kind !== 'dirty' && state.kind !== 'save_error') return state;
      const next =
        state.kind === 'dirty' ? state.nextContent : state.nextContent;
      return {
        kind: 'saving',
        savedContent: state.savedContent,
        inflightContent: next,
      };
    }

    case 'save_ok': {
      if (state.kind !== 'saving') return state;
      return { kind: 'idle', savedContent: state.inflightContent };
    }

    case 'save_failed': {
      if (state.kind !== 'saving') return state;
      return {
        kind: 'save_error',
        savedContent: state.savedContent,
        nextContent: state.inflightContent,
        error: action.error,
      };
    }

    default:
      return state;
  }
}

/**
 * Convenience for the status indicator: a small set of human-friendly
 * strings the header reads off the current state.
 */
export type StatusLabel = 'loading' | 'idle' | 'dirty' | 'saving' | 'save_error';

export function statusLabel(state: SaveState): StatusLabel {
  switch (state.kind) {
    case 'loading':
    case 'load_error':
      return 'loading';
    case 'idle':
      return 'idle';
    case 'dirty':
      return 'dirty';
    case 'saving':
      return 'saving';
    case 'save_error':
      return 'save_error';
  }
}
