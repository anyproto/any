import type { Meta, StoryObj } from '@storybook/react';
import { Provider, createStore } from 'jotai';
import { SpacesRail } from './SpacesRail';
import { activeSpaceIdAtom } from '@/atoms';

const meta = {
  title: 'Layout/SpacesRail',
  component: SpacesRail,
  parameters: { layout: 'fullscreen' },
  decorators: [
    (Story) => (
      <div style={{ height: '100vh', width: 280 }}>
        <Story />
      </div>
    ),
  ],
  tags: ['autodocs'],
} satisfies Meta<typeof SpacesRail>;

export default meta;
type Story = StoryObj<typeof meta>;

export const NoneActive: Story = {
  decorators: [
    (Story) => {
      const store = createStore();
      store.set(activeSpaceIdAtom, null);
      return (
        <Provider store={store}>
          <Story />
        </Provider>
      );
    },
  ],
};

export const Active: Story = {
  decorators: [
    (Story) => {
      const store = createStore();
      store.set(activeSpaceIdAtom, 'spc-anytype-team');
      return (
        <Provider store={store}>
          <Story />
        </Provider>
      );
    },
  ],
};
