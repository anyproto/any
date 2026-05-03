import type { Meta, StoryObj } from '@storybook/react';
import { Button } from './Button';
import { Input } from './Input';
import { Label } from './Label';
import { Popover, PopoverContent, PopoverTrigger } from './Popover';

const meta = { title: 'UI/Popover', parameters: { layout: 'centered' }, tags: ['autodocs'] } satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost">Edit name</Button>
      </PopoverTrigger>
      <PopoverContent>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <Label htmlFor="popover-name">Name</Label>
          <Input id="popover-name" defaultValue="Q3 Planning" />
        </div>
      </PopoverContent>
    </Popover>
  ),
};
