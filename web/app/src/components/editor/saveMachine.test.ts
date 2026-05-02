import { describe, it, expect } from 'vitest';
import { reduce, initial, statusLabel, type SaveState } from './saveMachine';
import { ApiError } from '@/lib/api/client';

const apiErr = new ApiError({ code: 'x', message: 'oops' }, 500);

describe('saveMachine reducer', () => {
  it('starts in loading', () => {
    expect(initial.kind).toBe('loading');
  });

  it('loading → idle on load_ok', () => {
    const next = reduce(initial, { type: 'load_ok', content: '# hi' });
    expect(next).toEqual({ kind: 'idle', savedContent: '# hi' });
  });

  it('loading → load_error on load_failed', () => {
    const next = reduce(initial, { type: 'load_failed', error: apiErr });
    expect(next).toEqual({ kind: 'load_error', error: apiErr });
  });

  it('idle → dirty on edit', () => {
    const s: SaveState = { kind: 'idle', savedContent: 'a' };
    const next = reduce(s, { type: 'edit', content: 'ab', now: 1 });
    expect(next).toEqual({
      kind: 'dirty',
      savedContent: 'a',
      nextContent: 'ab',
      since: 1,
    });
  });

  it('idle → idle when edit equals savedContent', () => {
    const s: SaveState = { kind: 'idle', savedContent: 'a' };
    const next = reduce(s, { type: 'edit', content: 'a', now: 1 });
    expect(next).toEqual(s);
  });

  it('dirty → saving on flush', () => {
    const s: SaveState = {
      kind: 'dirty',
      savedContent: 'a',
      nextContent: 'ab',
      since: 1,
    };
    const next = reduce(s, { type: 'flush' });
    expect(next).toEqual({
      kind: 'saving',
      savedContent: 'a',
      inflightContent: 'ab',
    });
  });

  it('saving → idle on save_ok', () => {
    const s: SaveState = {
      kind: 'saving',
      savedContent: 'a',
      inflightContent: 'ab',
    };
    const next = reduce(s, { type: 'save_ok' });
    expect(next).toEqual({ kind: 'idle', savedContent: 'ab' });
  });

  it('saving → save_error on save_failed', () => {
    const s: SaveState = {
      kind: 'saving',
      savedContent: 'a',
      inflightContent: 'ab',
    };
    const next = reduce(s, { type: 'save_failed', error: apiErr });
    expect(next).toEqual({
      kind: 'save_error',
      savedContent: 'a',
      nextContent: 'ab',
      error: apiErr,
    });
  });

  it('saving + edit → dirty (still has the in-flight save outstanding)', () => {
    const s: SaveState = {
      kind: 'saving',
      savedContent: 'a',
      inflightContent: 'ab',
    };
    // While saving, another edit lands → marks dirty against savedContent.
    const next = reduce(s, { type: 'edit', content: 'abc', now: 2 });
    expect(next).toEqual({
      kind: 'dirty',
      savedContent: 'a',
      nextContent: 'abc',
      since: 2,
    });
  });

  it('any state → loading on reload', () => {
    const s: SaveState = { kind: 'idle', savedContent: 'a' };
    expect(reduce(s, { type: 'reload' })).toEqual({ kind: 'loading' });
  });

  it('statusLabel maps every state', () => {
    expect(statusLabel({ kind: 'loading' })).toBe('loading');
    expect(statusLabel({ kind: 'load_error', error: apiErr })).toBe('loading');
    expect(statusLabel({ kind: 'idle', savedContent: '' })).toBe('idle');
    expect(
      statusLabel({ kind: 'dirty', savedContent: '', nextContent: 'x', since: 0 }),
    ).toBe('dirty');
    expect(
      statusLabel({ kind: 'saving', savedContent: '', inflightContent: 'x' }),
    ).toBe('saving');
    expect(
      statusLabel({
        kind: 'save_error',
        savedContent: '',
        nextContent: 'x',
        error: apiErr,
      }),
    ).toBe('save_error');
  });
});
