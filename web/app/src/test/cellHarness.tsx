import { type ReactElement } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

export function renderCell(ui: ReactElement) {
  return {
    user: userEvent.setup(),
    ...render(ui),
  };
}

export async function commitTextCell({
  user,
  button,
  next,
}: {
  user: ReturnType<typeof userEvent.setup>;
  button: string | RegExp;
  next: string;
}) {
  await user.click(screen.getByRole('button', { name: button }));
  const input = screen.getByRole('textbox');
  await user.clear(input);
  await user.type(input, `${next}{Enter}`);
}
