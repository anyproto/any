import type { Meta, StoryObj } from '@storybook/react';
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from './ContextMenu';

const meta = { title: 'UI/ContextMenu', parameters: { layout: 'centered' }, tags: ['autodocs'] } satisfies Meta;
export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
  render: () => (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          style={{
            padding: '24px 32px',
            border: '1px dashed currentColor',
            borderRadius: 6,
            opacity: 0.6,
          }}
        >
          Right-click here
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuLabel>Object</ContextMenuLabel>
        <ContextMenuItem>Open</ContextMenuItem>
        <ContextMenuItem>Rename…</ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem>Delete</ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  ),
};
