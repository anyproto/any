import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { axe } from 'vitest-axe';
import { Button } from './Button';

describe('<Button>', () => {
  it('renders text and triggers onClick', async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Save</Button>);
    const btn = screen.getByRole('button', { name: 'Save' });
    expect(btn).toBeInTheDocument();
    await userEvent.click(btn);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('respects disabled', async () => {
    const onClick = vi.fn();
    render(
      <Button onClick={onClick} disabled>
        Save
      </Button>,
    );
    await userEvent.click(screen.getByRole('button'));
    expect(onClick).not.toHaveBeenCalled();
  });

  it('renders primary, ghost, and danger variants', () => {
    const { rerender } = render(<Button variant="primary">x</Button>);
    expect(screen.getByRole('button')).toHaveClass('bg-accent');
    rerender(<Button variant="ghost">x</Button>);
    expect(screen.getByRole('button')).toHaveClass('bg-transparent');
    rerender(<Button variant="danger">x</Button>);
    expect(screen.getByRole('button')).toHaveClass('bg-destructive');
  });

  it('has no axe violations', async () => {
    const { container } = render(<Button>Save</Button>);
    expect(await axe(container)).toHaveNoViolations();
  });
});
