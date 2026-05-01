import { Toaster } from 'sonner';
import { HealthPage } from './pages/HealthPage';

/**
 * App shell for PR #1.
 *
 * Single route — the Health page (placeholder, replaced by the 3-pane
 * layout in PR #2). Toaster mounted globally so any component can
 * `import { toast } from 'sonner'` and surface a notification.
 */
export function App() {
  return (
    <>
      <HealthPage />
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
