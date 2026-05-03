import { describe, expect, it } from 'vitest';
import type { ObjectRecord } from '@/lib/api/objects';
import type { PropertyDef } from '@/lib/api/types';
import {
  formatListPropertyValue,
  patchTableRow,
  readTablePropValue,
} from './tableObjectValues';

function prop(kind: PropertyDef['kind'], xKey?: string): PropertyDef {
  return { id: 'p', kind, xKey };
}

describe('tableObjectValues', () => {
  it('reads and patches typed object namespaces without mutating the row', () => {
    const row: ObjectRecord = {
      id: 'obj-1',
      any: { name: 'Alien', types: ['t_movie'] },
      t_movie: { p_rating: 5 },
    };

    expect(readTablePropValue(row, 't_movie', 'p_rating')).toBe(5);

    const next = patchTableRow(row, 't_movie', 'p_rating', 6);
    expect(readTablePropValue(next, 't_movie', 'p_rating')).toBe(6);
    expect(readTablePropValue(row, 't_movie', 'p_rating')).toBe(5);
  });

  it('formats list previews by UI property kind', () => {
    expect(formatListPropertyValue(prop('string'), '  watched  ')).toBe('watched');
    expect(formatListPropertyValue(prop('number'), 5)).toBe('5');
    expect(formatListPropertyValue(prop('boolean'), false)).toBe('No');
    expect(formatListPropertyValue(prop('array', 'tags'), ['sci-fi', 7, 'classic'])).toBe(
      'sci-fi, classic',
    );
    expect(formatListPropertyValue(prop('null'), null)).toBeNull();
  });

  it('truncates long preview strings', () => {
    const value = 'x'.repeat(100);
    expect(formatListPropertyValue(prop('string'), value)).toBe(
      `${'x'.repeat(77)}...`,
    );
  });
});
