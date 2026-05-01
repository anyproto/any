import type { Preview } from '@storybook/react';
import '../src/styles/globals.css';

const preview: Preview = {
  parameters: {
    backgrounds: { disable: true }, // we use CSS tokens for bg
    controls: { expanded: true },
  },
  globalTypes: {
    theme: {
      description: 'Color theme',
      defaultValue: 'light',
      toolbar: {
        title: 'Theme',
        icon: 'circlehollow',
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
  },
  decorators: [
    (Story, ctx) => {
      const theme = (ctx.globals['theme'] as string) ?? 'light';
      document.documentElement.dataset['theme'] = theme;
      return <Story />;
    },
  ],
};

export default preview;
