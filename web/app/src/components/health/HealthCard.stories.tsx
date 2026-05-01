/* eslint-disable @typescript-eslint/no-explicit-any */
import type { Meta, StoryObj } from '@storybook/react';
import { HealthCard } from './HealthCard';
import { ApiError } from '@/lib/api/client';
import type { HealthResponse } from '@/lib/api/meta';

const sample: HealthResponse = {
  status: 'ok',
  version: 'any dev (commit abc1234)',
  startedAt: new Date().toISOString(),
  account: 'A9WYnfhbhUx7ik3FvjcCCBJvLkxn58thfCvt8qGZUJ99jgXJ',
};

// Stories don't need a real QueryClient — just shape-compatible objects.
const fakeQuery = (state: 'pending' | 'success' | 'error', data?: HealthResponse, error?: unknown): any => ({
  isPending: state === 'pending',
  isError: state === 'error',
  isSuccess: state === 'success',
  data,
  error: error ?? null,
  status: state,
});

const meta = {
  title: 'Health/HealthCard',
  component: HealthCard,
  parameters: { layout: 'padded' },
  tags: ['autodocs'],
} satisfies Meta<typeof HealthCard>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OK: Story = { args: { query: fakeQuery('success', sample) } };
export const Loading: Story = { args: { query: fakeQuery('pending') } };
export const Errored: Story = {
  args: {
    query: fakeQuery(
      'error',
      undefined,
      new ApiError({ code: 'server.unreachable', message: 'connection refused' }, 503),
    ),
  },
};
