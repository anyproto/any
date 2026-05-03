// Flat config (ESLint 9). The gates that matter are TypeScript strict
// mode + react-hooks + jsx-a11y. Prettier owns formatting.
import tseslint from 'typescript-eslint';
import react from 'eslint-plugin-react';
import reactHooks from 'eslint-plugin-react-hooks';
import jsxA11y from 'eslint-plugin-jsx-a11y';
import prettier from 'eslint-config-prettier';
import globals from 'globals';

export default tseslint.config(
  { ignores: ['dist', 'node_modules', 'coverage', 'storybook-static', 'playwright-report'] },
  ...tseslint.configs.recommended,
  ...tseslint.configs.stylistic,
  {
    files: ['src/**/*.{ts,tsx}', 'e2e/**/*.{ts,tsx}'],
    plugins: {
      react,
      'react-hooks': reactHooks,
      'jsx-a11y': jsxA11y,
    },
    languageOptions: {
      ecmaVersion: 2023,
      sourceType: 'module',
      globals: { ...globals.browser, ...globals.es2023 },
    },
    settings: {
      react: { version: '18.3' },
    },
    rules: {
      ...react.configs.recommended.rules,
      ...react.configs['jsx-runtime'].rules,
      ...reactHooks.configs.recommended.rules,
      ...jsxA11y.configs.recommended.rules,
      'react/prop-types': 'off',
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['@/atoms/*'],
              message: 'Use the public atoms barrel: @/atoms.',
            },
            {
              group: ['@/lib/api/objects/*'],
              message: 'Use the public object API barrel: @/lib/api/objects.',
            },
            {
              group: [
                '@/components/ui/*',
                '@/components/types/*',
                '@/components/health/*',
                '@/components/settings/*',
                '@/components/tree/*',
                '@/components/properties/*',
                '@/components/spaces/*',
                '@/components/objects/*',
                '@/components/tables/*',
              ],
              message:
                'Use the public component module barrel for cross-component imports.',
            },
            {
              group: [
                '@/components/tables/PropertyCell',
                '@/components/tables/cells/*',
              ],
              message:
                'Property editing lives in the shared properties module: @/components/properties.',
            },
            {
              group: ['@/shared/*'],
              message: 'Use the public shared barrel: @/shared.',
            },
          ],
        },
      ],
    },
  },
  prettier,
);
