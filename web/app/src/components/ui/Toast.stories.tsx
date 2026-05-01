import type { Meta, StoryObj } from '@storybook/react';
import { Toaster } from 'sonner';
import { Button } from './Button';
import { toast } from './Toast';

const meta = {
  title: 'UI/Toast',
  parameters: { layout: 'centered' },
  decorators: [
    (Story) => (
      <>
        <Story />
        <Toaster position="bottom-right" />
      </>
    ),
  ],
  tags: ['autodocs'],
} satisfies Meta;

export default meta;
type Story = StoryObj<typeof meta>;

export const Success: Story = {
  render: () => <Button onClick={() => toast.success('Saved')}>Show success</Button>,
};

export const ErrorToast: Story = {
  name: 'Error',
  render: () => (
    <Button variant="danger" onClick={() => toast.error('Network error')}>
      Show error
    </Button>
  ),
};
