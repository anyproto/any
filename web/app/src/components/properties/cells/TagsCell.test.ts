import { describe, it, expect } from 'vitest';
import { parseTags } from './TagsCell';

describe('parseTags', () => {
  it('trims, drops empties, dedupes, preserves first-seen order', () => {
    expect(parseTags('a, b, ,a')).toEqual(['a', 'b']);
    expect(parseTags('  hello , world  ')).toEqual(['hello', 'world']);
    expect(parseTags('x,x,x')).toEqual(['x']);
  });

  it('returns [] for empty / whitespace-only input', () => {
    expect(parseTags('')).toEqual([]);
    expect(parseTags(',,, ,')).toEqual([]);
  });

  it('preserves multi-word tags', () => {
    expect(parseTags('low carb, high protein')).toEqual(['low carb', 'high protein']);
  });
});
