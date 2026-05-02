import { describe, it, expect } from 'vitest';
import { rangeIds } from './tree-selection';

describe('rangeIds', () => {
  const siblings = [
    { id: 'a', nav: { pos: 'A' } },
    { id: 'b', nav: { pos: 'B' } },
    { id: 'c', nav: { pos: 'C' } },
    { id: 'd', nav: { pos: 'D' } },
  ];

  it('returns inclusive range, anchor first', () => {
    expect(rangeIds(siblings, 'b', 'd')).toEqual(['b', 'c', 'd']);
  });

  it('handles reverse direction', () => {
    expect(rangeIds(siblings, 'd', 'b')).toEqual(['b', 'c', 'd']);
  });

  it('single id when both ends are the same', () => {
    expect(rangeIds(siblings, 'b', 'b')).toEqual(['b']);
  });

  it('respects nav.pos order, not array order', () => {
    const shuffled = [siblings[2]!, siblings[0]!, siblings[3]!, siblings[1]!];
    expect(rangeIds(shuffled, 'a', 'c')).toEqual(['a', 'b', 'c']);
  });

  it('falls back to [toId] when anchor not found', () => {
    expect(rangeIds(siblings, 'missing', 'c')).toEqual(['c']);
  });
});
