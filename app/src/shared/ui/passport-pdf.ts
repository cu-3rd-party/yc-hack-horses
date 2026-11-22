// The viewer consumes the PDF.js API initialized by the matching core module.
import { AnnotationEditorType, AnnotationMode, getDocument, GlobalWorkerOptions } from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
import { EventBus, PDFViewer } from 'pdfjs-dist/web/pdf_viewer.mjs';
import 'pdfjs-dist/web/pdf_viewer.css';

GlobalWorkerOptions.workerSrc = workerUrl;

export function loadPassport(bytes: ArrayBuffer) {
  const assets = new URL('./pdfjs/', document.baseURI).href;
  return getDocument({
    data: new Uint8Array(bytes),
    cMapUrl: assets + 'cmaps/',
    standardFontDataUrl: assets + 'standard_fonts/',
    iccUrl: assets + 'iccs/',
    wasmUrl: assets + 'wasm/',
    useWorkerFetch: true,
  });
}

export function createReader(container: HTMLDivElement, signal: AbortSignal, onError: () => void) {
  const eventBus = new EventBus();
  const options = {
    container,
    eventBus,
    abortSignal: signal,
    annotationMode: AnnotationMode.ENABLE,
    annotationEditorMode: AnnotationEditorType.DISABLE,
    enableAutoLinking: false,
    removePageBorders: true,
  };
  const viewer = new PDFViewer(options);
  eventBus.on('pagesinit', () => {
    viewer.currentScaleValue = 'page-width';
  }, { signal });
  eventBus.on('pagerendered', (event: { error?: unknown }) => {
    if (event.error) onError();
  }, { signal });
  return viewer;
}
