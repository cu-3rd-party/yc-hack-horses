export type Point = [number, number];
export interface Annotation {
  outline: Point[];
  traits: { label: string; value: string; at: Point }[];
}
export interface Photo {
  id: string;
  name: string;
  source: string; // Bundled URL or IndexedDB media key.
  angle: string;
  framing: string;
  annotation: Annotation;
}
export interface Horse {
  id: string;
  name: string;
}
export interface TraitReading {
  label: string;
  value: string;
  confidence?: number;
}
export function describeTraits(readings: TraitReading[]) {
  return readings
    .map(
      (r) =>
        r.label +
        ' — ' +
        r.value +
        (r.confidence === undefined ? '' : ' (' + Math.round(r.confidence * 100) + '%)'),
    )
    .join(', ');
}
export interface Observation {
  id: string;
  horseId: string | null;
  name: string;
  date: string;
  photos: Photo[];
  coordinates: string;
  traits: TraitReading[];
  confidence?: number;
}
export interface Detection {
  id: string;
  number: number;
  annotation: Annotation;
  candidates: { horseId: string; score: number }[];
}
export interface Review {
  id: string;
  photo: Photo;
  detections: Detection[];
  illustrative: boolean;
}
export interface Notebook {
  horses: Horse[];
  observations: Observation[];
  review: Review | null;
  online: boolean;
  failNextSave: boolean;
  pending: string[];
  sent: number;
  mediaBytes: number;
  lastSync: string | null;
  syncError: string | null;
}
export interface ImportPhoto {
  filename: string;
  original: Blob;
  preview: Blob;
}
export type AnalysisInput = { sample: 'single' | 'pair' } | { upload: ImportPhoto };
export interface Decision {
  horseName?: string;
  reviewId: string;
  detectionId: string;
  horseId: string | null;
  name: string;
}
export interface ManualInput {
  name: string;
  horseId: string | null;
  coordinates: string;
  traits: TraitReading[];
  confidence: number;
  files: ImportPhoto[];
}
export const noMarks: Annotation = { outline: [], traits: [] };
export function validName(value: string) {
  const text = value.trim();
  return (
    !!text &&
    !/[<>:"/\\|?*]/.test(text) &&
    !Array.from(text).some((c) => c.charCodeAt(0) < 32) &&
    !/[. ]$/.test(value) &&
    !/^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)/i.test(text) &&
    new TextEncoder().encode(text).length <= 255
  );
}
export function horsePhotos(book: Notebook, id: string) {
  return book.observations.filter((o) => o.horseId === id).flatMap((o) => o.photos);
}
export function dateLabel(value: string) {
  return new Intl.DateTimeFormat('ru', { day: 'numeric', month: 'short', year: 'numeric' }).format(
    new Date(value),
  );
}
