import type { Meta, StoryObj } from '@storybook/react';
import { Provider, createStore } from 'jotai';
import { SpaceContents } from './SpaceContents';
import { activeSpaceIdAtom } from '@/atoms/selection';

const meta = {
  title: 'Layout/SpaceContents',
  component: SpaceContents,
  parameters: { layout: 'fullscreen' },
  decorators: [
    (Story) => (
      <div style={{ height: '100vh', width: 320 }}>
        <Story />
      </div>
    ),
  ],
  tags: ['autodocs'],
} satisfies Meta<typeof SpaceContents>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
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

export const AnytypeTeam: Story = {
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
