import { useEffect, useRef, useState } from 'react';
import { LoaderCircle, ZoomIn, ZoomOut } from 'lucide-react';
import type { PDFDocumentLoadingTask } from 'pdfjs-dist';
import type { PDFViewer } from 'pdfjs-dist/web/pdf_viewer.mjs';

export function PassportReader({ url }: { url: string }) {
  const container = useRef<HTMLDivElement>(null);
  const reader = useRef<PDFViewer | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [ready, setReady] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    const controller = new AbortController();
    const { signal } = controller;
    let task: PDFDocumentLoadingTask | undefined;
    setReady(false);
    setError('');

    async function open() {
      const response = await fetch(url, { signal });
      if (signal.aborted) return;
      if (response.status === 404) {
        setError('Файл паспорта пока недоступен');
        return;
      }
      if (!response.ok) throw new Error('Passport request failed');
      const [pdf, bytes] = await Promise.all([import('./passport-pdf'), response.arrayBuffer()]);
      if (signal.aborted) return;
      task = pdf.loadPassport(bytes);
      const passport = await task.promise;
      await passport.getPage(1);
      if (signal.aborted || !container.current) return;
      const viewer = pdf.createReader(container.current, signal, () => {
        setError('Не удалось открыть PDF');
      });
      reader.current = viewer;
      viewer.setDocument(passport);
      await viewer.firstPagePromise;
      if (!signal.aborted) setReady(true);
    }

    void open().catch(() => {
      if (!signal.aborted) setError('Не удалось открыть PDF');
    });
    return () => {
      controller.abort();
      reader.current?.setDocument(null);
      reader.current = null;
      void task?.destroy();
    };
  }, [url, attempt]);

  return (
    <>
      <div className="passport-body">
        <div
          ref={container}
          className="passport-scroll"
          role="region"
          aria-label="Страницы паспорта"
          tabIndex={ready && !error ? 0 : -1}
          hidden={!!error}
        >
          <div className="pdfViewer" />
        </div>
        {error ? (
          <div className="passport-status" role="alert">
            {error}
            <button className="text-link" onClick={() => setAttempt(value => value + 1)}>
              Повторить
            </button>
          </div>
        ) : !ready && (
          <div className="passport-status" role="status">
            <LoaderCircle size={18} className="spin" /> Загружаем паспорт…
          </div>
        )}
      </div>
      {ready && !error && (
        <div className="passport-zoom" role="group" aria-label="Масштаб паспорта">
          <button aria-label="Уменьшить масштаб" onClick={() => reader.current?.decreaseScale()}>
            <ZoomOut size={18} />
          </button>
          <button onClick={() => {
            if (reader.current) reader.current.currentScaleValue = 'page-width';
          }}>
            По ширине
          </button>
          <button aria-label="Увеличить масштаб" onClick={() => reader.current?.increaseScale()}>
            <ZoomIn size={18} />
          </button>
        </div>
      )}
    </>
  );
}
