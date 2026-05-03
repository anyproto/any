import type { Meta, StoryObj } from '@storybook/react';
import { Button } from './Button';
import { SimpleTooltip } from './Tooltip';

const meta = { title: 'UI/Tooltip', parameters: { layout: 'centered' }, tags: ['autodocs'] } satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <SimpleTooltip text="Save changes (⌘S)">
      <Button>Save</Button>
    </SimpleTooltip>
  ),
};
