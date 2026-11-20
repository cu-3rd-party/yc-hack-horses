import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import App from './app/App';
import './index.css';

// В Tauri WebView используется MemoryRouter (нет адресной строки),
// в браузере — BrowserRouter, см. App.
createRoot(document.getElementById('root') as HTMLElement).render(
  <StrictMode>
    <MemoryRouter>
      <App />
    </MemoryRouter>
  </StrictMode>,
);
