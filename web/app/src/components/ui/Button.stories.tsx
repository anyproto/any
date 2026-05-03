import type { Meta, StoryObj } from '@storybook/react';
import { Button } from './Button';

const meta = {
  title: 'UI/Button',
  component: Button,
  args: { children: 'Save' },
  argTypes: {
    variant: { control: 'select', options: ['primary', 'ghost', 'danger'] },
    size: { control: 'select', options: ['sm', 'md'] },
    disabled: { control: 'boolean' },
  },
  parameters: { layout: 'centered' },
  tags: ['autodocs'],
} satisfies Meta<typeof Button>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Primary: Story = {};
export const Ghost: Story = { args: { variant: 'ghost' } };
export const Danger: Story = { args: { variant: 'danger' } };
export const Small: Story = { args: { size: 'sm' } };
export const Disabled: Story = { args: { disabled: true } };

export const AllVariants: Story = {
  render: () => (
    <div style={{ display: 'flex', gap: 12 }}>
      <Button variant="primary">Primary</Button>
      <Button variant="ghost">Ghost</Button>
      <Button variant="danger">Danger</Button>
    </div>
  ),
};
