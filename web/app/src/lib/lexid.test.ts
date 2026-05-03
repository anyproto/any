import { describe, it, expect } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { CHARS_ALL_NO_ESCAPE, nav } from './lexid';

interface Fixture {
  op: 'Middle' | 'Next' | 'NextBefore' | 'CharsAllNoEscape';
  args: (string | number)[];
  expected: string;
}

// Walk up from this file looking for docs/fixtures/lexid.json so the
// test works whether the project is mounted at the canonical location
// or under a sandbox path.
function findFixtures(): string {
  let dir = __dirname;
  for (let i = 0; i < 8; i++) {
    const candidate = resolve(dir, 'docs/fixtures/lexid.json');
    if (existsSync(candidate)) return candidate;
    const parent = dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error('docs/fixtures/lexid.json not found anywhere up the tree');
}

const fixtures = JSON.parse(readFileSync(findFixtures(), 'utf8')) as Fixture[];

describe('lexid TS port matches Go fixtures', () => {
  it('CharsAllNoEscape constant', () => {
    const fx = fixtures.find((f) => f.op === 'CharsAllNoEscape');
    expect(fx).toBeDefined();
    expect(CHARS_ALL_NO_ESCAPE).toBe(fx!.expected);
  });

  it('Middle()', () => {
    const fx = fixtures.find((f) => f.op === 'Middle');
    expect(fx).toBeDefined();
    expect(nav.middle()).toBe(fx!.expected);
  });

  it('Next(prev) — every fixture row', () => {
    const next = fixtures.filter((f) => f.op === 'Next');
    expect(next.length).toBeGreaterThan(0);
    for (const fx of next) {
      const [prev] = fx.args as [string];
      expect(nav.next(prev), `Next(${JSON.stringify(prev)})`).toBe(fx.expected);
    }
  });

  it('NextBefore(prev, before) — every fixture row', () => {
    const nb = fixtures.filter((f) => f.op === 'NextBefore');
    expect(nb.length).toBeGreaterThan(0);
    for (const fx of nb) {
      const [prev, before] = fx.args as [string, string];
      expect(
        nav.nextBefore(prev, before),
        `NextBefore(${JSON.stringify(prev)}, ${JSON.stringify(before)})`,
      ).toBe(fx.expected);
    }
  });
});

describe('lexid invariants', () => {
  it('Next(x) is strictly greater than x', () => {
    let prev = nav.middle();
    for (let i = 0; i < 50; i++) {
      const next = nav.next(prev);
      expect(next > prev).toBe(true);
      prev = next;
    }
  });

  it('NextBefore(a, b) lands strictly between a and b', () => {
    const a = 'PPPP';
    const b = 'PPPR';
    const out = nav.nextBefore(a, b);
    expect(a < out).toBe(true);
    expect(out < b).toBe(true);
  });

  it('NextBefore throws when before <= prev', () => {
    expect(() => nav.nextBefore('PPPP', 'PPPP')).toThrow(/incorrect before/i);
    expect(() => nav.nextBefore('PPPR', 'PPPP')).toThrow(/incorrect before/i);
  });
});
