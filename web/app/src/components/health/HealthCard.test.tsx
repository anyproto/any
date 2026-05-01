import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { axe } from 'vitest-axe';
import { HealthCard } from './HealthCard';
import { ApiError } from '@/lib/api/client';
import type { HealthResponse } from '@/lib/api/meta';
import type { UseQueryResult } from '@tanstack/react-query';

function loadingQuery(): UseQueryResult<HealthResponse> {
  return {
    isPending: true,
    isError: false,
    isSuccess: false,
    data: undefined,
    error: null,
    status: 'pending',
  } as unknown as UseQueryResult<HealthResponse>;
}

function errorQuery(error: unknown): UseQueryResult<HealthResponse> {
  return {
    isPending: false,
    isError: true,
    isSuccess: false,
    data: undefined,
    error,
    status: 'error',
  } as unknown as UseQueryResult<HealthResponse>;
}

function okQuery(data: HealthResponse): UseQueryResult<HealthResponse> {
  return {
    isPending: false,
    isError: false,
    isSuccess: true,
    data,
    error: null,
    status: 'success',
  } as unknown as UseQueryResult<HealthResponse>;
}

const sampleOk: HealthResponse = {
  status: 'ok',
  version: 'any dev (commit none)',
  startedAt: '2026-05-01T19:08:36Z',
  account: 'A9WYnfhbhUx7ik3FvjcCCBJvLkxn58thfCvt8qGZUJ99jgXJ',
};

describe('<HealthCard>', () => {
  it('renders the loading state', () => {
    const { container } = render(<HealthCard query={loadingQuery()} />);
    expect(screen.getByText(/checking/i)).toBeInTheDocument();
    expect(container.querySelector('.animate-pulse')).toBeTruthy();
  });

  it('renders the error state with code + message', () => {
    const err = new ApiError(
      { code: 'server.unreachable', message: 'connection refused' },
      503,
    );
    render(<HealthCard query={errorQuery(err)} />);
    expect(screen.getByText('server.unreachable')).toBeInTheDocument();
    expect(screen.getByText('connection refused')).toBeInTheDocument();
  });

  it('renders the ok state with account, version, and started', () => {
    render(<HealthCard query={okQuery(sampleOk)} />);
    expect(screen.getByText(/Server: ok/)).toBeInTheDocument();
    expect(
      screen.getByText('A9WYnfhbhUx7ik3FvjcCCBJvLkxn58thfCvt8qGZUJ99jgXJ'),
    ).toBeInTheDocument();
    expect(screen.getByText('any dev (commit none)')).toBeInTheDocument();
  });

  it('has no axe violations across all three states', async () => {
    for (const q of [loadingQuery(), errorQuery(new Error('x')), okQuery(sampleOk)]) {
      const { container, unmount } = render(<HealthCard query={q} />);
      expect(await axe(container)).toHaveNoViolations();
      unmount();
    }
  });
});
