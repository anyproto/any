import '@testing-library/jest-dom/vitest';
import { expect, afterEach } from 'vitest';
import { cleanup } from '@testing-library/react';
import * as axeMatchers from 'vitest-axe/matchers';

// Make `expect(await axe(node)).toHaveNoViolations()` available.
expect.extend(axeMatchers);

// Reset the JSDOM tree between tests.
afterEach(() => {
  cleanup();
});

// jsdom doesn't ship matchMedia; theme.ts and Radix Tooltip rely on it.
if (typeof window !== 'undefined' && typeof window.matchMedia === 'undefined') {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }),
  });
}
