import { Toaster } from 'sonner';
import { AppShell } from './components/layout/AppShell';

/**
 * App shell. Renders the 3-pane layout (PR #2). The legacy single-page
 * HealthPage was retired; the health card now lives in pane 3's empty
 * state until PR #3 lands a real Settings surface.
 */
export function App() {
  return (
    <>
      <AppShell />
      <Toaster
        position="bottom-right"
        toastOptions={{
          // Tokens come from globals.css; sonner renders into a
          // shadow root so we pass colors via inline style.
          style: {
            background: 'var(--color-background)',
            color: 'var(--color-foreground)',
            border: '1px solid color-mix(in oklab, var(--color-foreground) 12%, transparent)',
          },
        }}
      />
    </>
  );
}
