import { describe, expect, it } from 'vitest';
import { keyOf } from './keys';

describe('keyOf', () => {
  it('preserves tuple boundaries for opaque ids', () => {
    expect(keyOf('space:type', 'object')).not.toBe(keyOf('space', 'type:object'));
  });
});
