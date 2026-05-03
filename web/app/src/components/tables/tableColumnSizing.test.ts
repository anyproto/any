import { describe, expect, it } from 'vitest';
import type { ObjectRecord } from '@/lib/api/objects';
import type { PropertyDef } from '@/lib/api/types';
import { autoFitPropertyColumnWidth } from './tableColumnSizing';

function row(id: string, props: Record<string, unknown> = {}): ObjectRecord {
  return {
    id,
    any: { name: id, types: ['t_movie'] },
    nav: { type: 1, parentId: '', pos: id },
    t_movie: props,
  };
}

describe('tableColumnSizing', () => {
  it('auto-fits an empty property column from the header instead of dash placeholders', () => {
    const prop: PropertyDef = { id: 'p_author', name: 'Author', kind: 'string' };
    const width = autoFitPropertyColumnWidth({
      rows: [
        row('obj-1'),
        row('obj-2', { p_author: '' }),
        row('obj-3', { p_author: null }),
      ],
      typeId: 't_movie',
      prop,
    });

    expect(width).toBeGreaterThanOrEqual(96);
    expect(width).toBeLessThan(140);
  });

  it('still expands to fit real property content', () => {
    const prop: PropertyDef = { id: 'p_author', name: 'Author', kind: 'string' };
    const width = autoFitPropertyColumnWidth({
      rows: [
        row('obj-1', {
          p_author: 'Arkady and Boris Strugatsky',
        }),
      ],
      typeId: 't_movie',
      prop,
    });

    expect(width).toBeGreaterThan(220);
  });
});
