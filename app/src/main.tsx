import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import App from './app/App';
import { Toasts } from './shared/ui/journal-ui';
import './index.css';
const client = new QueryClient({
  defaultOptions: {
    queries: { networkMode: 'always', retry: false, refetchOnWindowFocus: false },
    mutations: { networkMode: 'always', retry: false },
  },
});
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <Toasts>
        <App />
      </Toasts>
    </QueryClientProvider>
  </StrictMode>,
);
