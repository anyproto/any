import type { Meta, StoryObj } from '@storybook/react';
import { Provider, createStore } from 'jotai';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ObjectView } from './ObjectView';
import { activeObjectIdAtom } from '@/atoms/selection';

const qc = new QueryClient({
  defaultOptions: { queries: { retry: false } },
});

const meta = {
  title: 'Layout/ObjectView',
  component: ObjectView,
  parameters: { layout: 'fullscreen' },
  decorators: [
    (Story) => (
      <QueryClientProvider client={qc}>
        <div style={{ height: '100vh' }}>
          <Story />
        </div>
      </QueryClientProvider>
    ),
  ],
  tags: ['autodocs'],
} satisfies Meta<typeof ObjectView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
  decorators: [
    (Story) => {
      const store = createStore();
      store.set(activeObjectIdAtom, null);
      return (
        <Provider store={store}>
          <Story />
        </Provider>
      );
    },
  ],
};

export const Selected: Story = {
  decorators: [
    (Story) => {
      const store = createStore();
      store.set(activeObjectIdAtom, 'obj-random');
      return (
        <Provider store={store}>
          <Story />
        </Provider>
      );
    },
  ],
};
