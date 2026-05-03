# Playbook: Add a UI primitive

For new primitives in `web/app/src/components/ui/` (Button, Input,
Dialog, etc.). Not for feature components — see
[`frontend-feature.md`](./frontend-feature.md).

## Steps

1. **File location.** Each primitive is one `.tsx` file in
   `web/app/src/components/ui/`. Forward refs from the underlying DOM
   element or Radix root.

2. **Variants via cva.** Multiple looks (sizes, styles) are defined
   with `class-variance-authority`:

   ```ts
   const button = cva([...base classes], {
     variants: {
       variant: { primary: '...', ghost: '...' },
       size:    { sm: '...', md: '...' },
     },
     defaultVariants: { variant: 'primary', size: 'md' },
   });
   ```

3. **Compose with `cn()`.** All className construction goes through
   `cn(...)` from `@/lib/cn` so caller utilities can override defaults
   via `tailwind-merge`.

4. **Tokens, not raw colors.** Reference tokens via Tailwind utilities
   (`bg-background`, `text-foreground`, `text-foreground/70`,
   `text-destructive`). Never use hex codes; never introduce a new
   color token without updating `src/styles/tokens.css` and
   `docs/agents/README.md`.

5. **Focus ring.** Every interactive primitive must have a visible
   `:focus-visible` ring using `--color-accent`. The standard class
   set is:

   ```
   focus-visible:outline-none focus-visible:ring-2
   focus-visible:ring-accent focus-visible:ring-offset-2
   focus-visible:ring-offset-background
   ```

6. **Export.** Add to `src/components/ui/index.ts` (the barrel). Also
   re-export the prop types if consumers will need them.

## Required files for a primitive named `Foo`

- `src/components/ui/Foo.tsx` — the component.
- `src/components/ui/Foo.test.tsx` — Vitest + RTL + `vitest-axe`.
  Cover: renders, every variant, keyboard interaction, no axe violations.
- `src/components/ui/Foo.stories.tsx` — Storybook. One story per
  variant + at least one composition. Tag `'autodocs'`.
- `src/components/ui/index.ts` — add the exports.

## Test template

```tsx
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { axe } from 'vitest-axe';
import { Foo } from './Foo';

describe('<Foo>', () => {
  it('renders', () => {
    render(<Foo>x</Foo>);
    expect(screen.getByRole(/* … */)).toBeInTheDocument();
  });

  it('has no axe violations', async () => {
    const { container } = render(<Foo>x</Foo>);
    expect(await axe(container)).toHaveNoViolations();
  });
});
```

## Don't

- Don't bring in another component library (MUI, Mantine, shadcn/ui
  copy-paste). The whole point of this folder is that we own the
  primitives.
- Don't put feature-specific logic in `ui/`. If the component needs
  to know about spaces / objects / types, it lives in
  `src/components/<feature>/` instead.
- Don't reach for `useEffect` for state that could be derived. If
  state lives across components, prefer a Jotai atom under
  `src/atoms/`.
