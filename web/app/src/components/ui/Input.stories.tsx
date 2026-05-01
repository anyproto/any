import type { Meta, StoryObj } from '@storybook/react';
import { Input, Textarea } from './Input';
import { Label } from './Label';

const meta = {
  title: 'UI/Input',
  component: Input,
  parameters: { layout: 'centered' },
  tags: ['autodocs'],
} satisfies Meta<typeof Input>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = { args: { placeholder: 'Type here…' } };
export const Disabled: Story = { args: { disabled: true, placeholder: 'Disabled' } };

export const WithLabel: Story = {
  render: () => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4, width: 280 }}>
      <Label htmlFor="space-name">Space name</Label>
      <Input id="space-name" placeholder="My space" />
    </div>
  ),
};

export const TextareaStory: StoryObj = {
  name: 'Textarea',
  render: () => <Textarea placeholder="Long text…" rows={5} style={{ width: 280 }} />,
};
