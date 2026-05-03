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

// One store, shared between the pre-mount theme effect and the React
// tree. JotaiProvider with no `store` prop creates a *new* store —
// then `useAtom(themePreferenceAtom)` inside components writes to a
// different store than the one installThemeEffect is watching, and
// the DOM never updates. Pass the same default store to both.
const store = getDefaultStore();
installThemeEffect(store);

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <JotaiProvider store={store}>
        <App />
      </JotaiProvider>
    </QueryClientProvider>
  </StrictMode>,
);
