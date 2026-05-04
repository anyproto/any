import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Provider, createStore } from 'jotai';
import { AppSettingsDialog } from './AppSettingsDialog';
import { editorEngineAtom, settingsOpenAtom, themePreferenceAtom } from '@/atoms';

describe('<AppSettingsDialog>', () => {
  it('lets the user choose system, white, or dark theme', async () => {
    const store = createStore();
    store.set(settingsOpenAtom, true);
    store.set(themePreferenceAtom, 'system');

    render(
      <Provider store={store}>
        <AppSettingsDialog />
      </Provider>,
    );

    expect(screen.getByRole('dialog', { name: 'Settings' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /system/i })).toHaveAttribute(
      'aria-checked',
      'true',
    );

    await userEvent.click(screen.getByRole('radio', { name: /dark/i }));
    expect(store.get(themePreferenceAtom)).toBe('dark');

    await userEvent.click(screen.getByRole('radio', { name: /white/i }));
    expect(store.get(themePreferenceAtom)).toBe('light');
  });

  it('lets the user choose the editor engine', async () => {
    const store = createStore();
    store.set(settingsOpenAtom, true);

    render(
      <Provider store={store}>
        <AppSettingsDialog />
      </Provider>,
    );

    expect(
      screen.getByRole('radiogroup', { name: /editor engine/i }),
    ).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: /lexical/i })).toHaveAttribute(
      'aria-checked',
      'true',
    );

    await userEvent.click(screen.getByRole('radio', { name: /blocknote/i }));
    expect(store.get(editorEngineAtom)).toBe('blocknote');
  });
});
