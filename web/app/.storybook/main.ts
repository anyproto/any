import type { StorybookConfig } from '@storybook/react-vite';

const config: StorybookConfig = {
  stories: ['../src/**/*.stories.@(ts|tsx)'],
  addons: ['@storybook/addon-essentials', '@storybook/addon-a11y'],
  framework: { name: '@storybook/react-vite', options: {} },
  typescript: {
    reactDocgen: 'react-docgen-typescript',
  },
  docs: { autodocs: 'tag' },
};

export default config;
