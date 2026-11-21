import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import App from './app/App';
import { DialogHost, Toasts } from './shared/ui/journal-ui';
import { initialScheme } from './app/appearance';
import './index.css';
document.documentElement.dataset.scheme = initialScheme();
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
        <DialogHost>
          <App />
        </DialogHost>
      </Toasts>
    </QueryClientProvider>
  </StrictMode>,
);
