import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { SectionHeader } from './SectionHeader';
import { SpaceAvatar } from './SpaceAvatar';

describe('<SectionHeader>', () => {
  it('toggles from the header button and renders the count', async () => {
    const onToggle = vi.fn();
    render(
      <SectionHeader label="Pages" count={3} expanded={false} onToggle={onToggle} />,
    );

    const button = screen.getByRole('button', { name: 'Pages' });
    expect(button).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByText('3')).toBeInTheDocument();

    await userEvent.click(button);
    expect(onToggle).toHaveBeenCalled();
  });

  it('lets custom actions handle their own clicks', async () => {
    const onToggle = vi.fn();
    const onAction = vi.fn();
    render(
      <SectionHeader
        label="Lists"
        expanded
        onToggle={onToggle}
        action={
          <button type="button" onClick={onAction}>
            Add
          </button>
        }
      />,
    );

    await userEvent.click(screen.getByRole('button', { name: 'Add' }));

    expect(onAction).toHaveBeenCalled();
    expect(onToggle).not.toHaveBeenCalled();
  });
});

describe('<SpaceAvatar>', () => {
  it('uses the explicit icon override instead of the generated glyph', () => {
    render(
      <SpaceAvatar
        spaceId="spc-one"
        name="Marketing"
        iconOverride="M2"
        active
        size="sm"
      />,
    );

    expect(screen.getByText('M2')).toBeInTheDocument();
  });
});
