/**
 * TypeScript port of github.com/anyproto/lexid v0.0.6.
 *
 * Mirrors the Go implementation byte-for-byte under the alphabet
 * `CharsAllNoEscape` with `blockSize=4` and `stepSize=100` — the
 * combination the server uses (see `internal/nav/nav.go`). Tests
 * compare against fixtures generated from the Go side at
 * `docs/fixtures/lexid.json`.
 *
 * We expose a single `nav` instance pre-configured with the server's
 * parameters; consumers should import that and not new up their own.
 *
 * Only the operations the client needs are ported:
 *   - Middle()   — first id in an empty range
 *   - Next(prev) — append at the end (default step of stepSize)
 *   - NextBefore(prev, before) — between two ids
 */

export const CHARS_ALL_NO_ESCAPE =
  "!#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[]^_`abcdefghijklmnopqrstuvwxyz{|}~";

export class Lexid {
  /** Sorted unique chars, byte-equivalent to Go's []byte. */
  readonly chars: string;
  readonly blockSize: number;
  readonly stepSize: number;

  /** charIndex[byte] → index in chars, or -1 for unknown bytes. */
  private readonly charIndex: number[];
  /** nextChar[byte] → next byte in the cycle. */
  private readonly nextChar: number[];
  private readonly lower: number;

  constructor(chars: string, blockSize: number, stepSize: number) {
    if (blockSize < 1) blockSize = 1;
    if (stepSize < 1) stepSize = 1;

    // Dedup while keeping the *first occurrence* of each byte (Go does
    // the same with the seen-map).
    const seen = new Array<boolean>(256).fill(false);
    const uniqueBytes: number[] = [];
    for (let i = 0; i < chars.length; i++) {
      const b = chars.charCodeAt(i);
      if (!seen[b]) {
        seen[b] = true;
        uniqueBytes.push(b);
      }
    }
    if (uniqueBytes.length < 2) {
      throw new Error('chars must contain at least two unique characters');
    }

    // Capacity sanity check — port of the Go overflow-safe loop.
    let maxCapacity = 1;
    const N = uniqueBytes.length;
    let overflow = false;
    for (let i = 0; i < blockSize; i++) {
      if (maxCapacity > Math.floor(Number.MAX_SAFE_INTEGER / N)) {
        overflow = true;
        break;
      }
      maxCapacity *= N;
    }
    if (!overflow && stepSize >= maxCapacity) {
      throw new Error(
        `stepSize (${stepSize}) must be less than block capacity (${maxCapacity})`,
      );
    }

    uniqueBytes.sort((a, b) => a - b);
    this.chars = String.fromCharCode(...uniqueBytes);
    this.blockSize = blockSize;
    this.stepSize = stepSize;
    this.lower = uniqueBytes[0]!;

    this.nextChar = new Array<number>(256).fill(this.lower);
    this.charIndex = new Array<number>(256).fill(-1);
    for (let i = 0; i < uniqueBytes.length; i++) {
      const c = uniqueBytes[i]!;
      this.nextChar[c] =
        i < uniqueBytes.length - 1 ? uniqueBytes[i + 1]! : uniqueBytes[0]!;
      this.charIndex[c] = i;
    }
  }

  // ---------------- Public API --------------------------------------

  middle(): string {
    const mid = Math.floor(this.chars.length / 2);
    const c = this.chars.charCodeAt(mid);
    const out = new Array<number>(this.blockSize).fill(c);
    return String.fromCharCode(...out);
  }

  next(prev: string): string {
    return this.nextStep(prev, this.stepSize);
  }

  nextBefore(prev: string, before: string): string {
    if (before <= prev) {
      throw new Error(
        `incorrect before value: '${before}' less or equal '${prev}'`,
      );
    }

    let prevPad = prev;
    let beforePad = before;
    let pad = this.blockSize - (prevPad.length % this.blockSize);
    if (pad !== this.blockSize) prevPad = this.padding(prevPad, pad);
    pad = this.blockSize - (beforePad.length % this.blockSize);
    if (pad !== this.blockSize) beforePad = this.padding(beforePad, pad);

    if (prev === '' || before.startsWith(prev)) {
      const beforeTail = before.slice(prev.length);
      // If the tail is the min possible value, increase prev's padding.
      if (beforeTail === this.padding('', beforeTail.length)) {
        const padN = this.blockSize * Math.floor(beforePad.length / this.blockSize);
        prevPad = this.padding(prevPad, padN);
        if (prevPad === beforePad) {
          prevPad = this.padding('', padN + this.blockSize);
        }
      }
    }

    const lDiff = prevPad.length - beforePad.length;
    if (lDiff > 0) beforePad = this.padding(beforePad, lDiff);
    else if (lDiff < 0) prevPad = this.padding(prevPad, -lDiff);

    const dist = this.approxDistance(prevPad, beforePad);
    if (dist > 0) {
      let step = this.stepSize;
      while (step > 0 && step / dist > 0.3) {
        step = Math.floor(step / 2);
      }
      if (step > 0) {
        const next = this.nextStep(prevPad, step);
        if (next < before) return next;
      }
    }
    const next = this.addTail(prevPad);
    if (prev > next || next > before) {
      throw new Error(
        `unable to create id between '${prev}' and '${before}'; result='${next}'`,
      );
    }
    return next;
  }

  // ---------------- Internals ---------------------------------------

  private nextStep(prev: string, step: number): string {
    if (prev === '') {
      const firstId = new Array<number>(this.blockSize);
      for (let i = 0; i < this.blockSize; i++) {
        firstId[i] = i === this.blockSize - 1 ? this.nextChar[this.lower]! : this.lower;
      }
      prev = String.fromCharCode(...firstId);
    }
    const padN = this.blockSize - (prev.length % this.blockSize);
    if (padN !== this.blockSize) {
      return this.padding(prev, padN);
    }

    const bytes: number[] = [];
    for (let i = 0; i < prev.length; i++) bytes.push(prev.charCodeAt(i));

    // Two-level loop — same shape as the Go `goto doSteps`.
    let carry = 0;
    while (true) {
      doSteps: {
        for (let s = 0; s < step; s++) {
          carry = 1;
          for (let i = bytes.length - 1; i >= 0; i--) {
            if (carry === 0) break;
            const newValue = this.nextChar[bytes[i]!]!;
            if (newValue === this.lower) {
              if (i === bytes.length - 1) {
                bytes[i] = this.nextChar[this.lower]!;
              } else {
                bytes[i] = newValue;
              }
              carry = 1;
            } else {
              bytes[i] = newValue;
              carry = 0;
            }
          }
          if (carry !== 0) break;
        }
        if (carry !== 0) {
          // Emulate `prev = padding; prevBytes = bytes; goto doSteps`.
          const padded = this.padding(String.fromCharCode(...bytes), this.blockSize);
          bytes.length = 0;
          for (let i = 0; i < padded.length; i++) bytes.push(padded.charCodeAt(i));
          break doSteps;
        }
        return String.fromCharCode(...bytes);
      }
    }
  }

  private padding(s: string, pad: number): string {
    const bytes: number[] = [];
    for (let i = 0; i < s.length; i++) bytes.push(s.charCodeAt(i));
    for (let i = 0; i < pad; i++) {
      bytes.push(i === pad - 1 ? this.nextChar[this.lower]! : this.lower);
    }
    return String.fromCharCode(...bytes);
  }

  private approxDistance(a: string, b: string): number {
    const size = Math.min(a.length, b.length);
    let distance = 0;
    let multiplier = 1;
    for (let i = size - 1; i >= 0; i--) {
      const idxA = this.charIndex[a.charCodeAt(i)]!;
      const idxB = this.charIndex[b.charCodeAt(i)]!;
      distance += (idxB - idxA) * multiplier;
      multiplier *= this.chars.length;
    }
    return distance;
  }

  private addTail(prev: string): string {
    const middle = Math.floor(this.chars.length / 2);
    const tailed = prev + this.chars[middle]!;
    return this.padding(tailed, this.blockSize - 1);
  }
}

/**
 * The shared instance — same parameters as the server's
 * `internal/nav/nav.go` lexidGen. Always import this; don't new up
 * a fresh Lexid in app code.
 */
export const nav = new Lexid(CHARS_ALL_NO_ESCAPE, 4, 100);

/**
 * Convenience matching `nav.NextPos` server-side: empty string yields
 * Middle(), otherwise Next(prev). Used when computing a drop-at-end
 * pos for an empty target folder.
 */
export function nextPos(prev: string): string {
  if (prev === '') return nav.middle();
  return nav.next(prev);
}
