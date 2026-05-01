import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { Provider as JotaiProvider, getDefaultStore } from 'jotai';

import { App } from './App';
import { queryClient } from './lib/queryClient';
import { installThemeEffect } from './atoms/theme';
import './styles/globals.css';

const root = document.getElementById('root');
if (!root) {
  throw new Error('No #root element in index.html');
}

// Theme is global state — install the matchMedia + data-theme effect
// against the default Jotai store so it works before React mounts.
installThemeEffect(getDefaultStore());

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <JotaiProvider>
        <App />
      </JotaiProvider>
    </QueryClientProvider>
  </StrictMode>,
);
