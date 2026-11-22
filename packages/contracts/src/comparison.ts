// Go /api/v1 comparison contract. All geometry uses the original image axes.
export type ImagePoint = [number, number];
export interface PartMask {
  png: string; // Base64 indexed PNG with transparent background and anatomical part colors.
  box: [number, number, number, number]; // Left, top, right, bottom in normalized image axes.
}
export interface HorseAnnotation {
  part_mask?: PartMask;
  horse_polygon: ImagePoint[];
  traits: { key: string; value: string; polygon: ImagePoint[] | null }[];
}
export interface ComparisonRequest {
  blob_sha256: string;
  top_k: number;
  capture_date?: string;
}
export interface ComparisonResult {
  comparison_id: string;
  blob_sha256: string;
  detections: {
    detection_id: string;
    detection_confidence: number;
    annotation: HorseAnnotation;
    candidates: {
      horse_id: string;
      name: string;
      cosine_similarity: number;
      best_photo_id: string;
      reference_count?: number;
      season_fallback?: boolean;
    }[];
  }[];
}
export type ComparisonDecision = { observation_name: string; comment?: string } & (
  | { action: 'match'; horse_id: string }
  | { action: 'create_horse'; horse_name: string }
);
export interface DecisionResult {
  observation_id: string;
  horse_id: string;
  remaining_detection_ids: string[];
}
export interface CancelResult {
  status: 'cancelled';
  saved_observation_ids: string[];
  skipped_detection_ids: string[];
}
export interface ObservationPhoto {
  photo_id: string;
  blob_sha256: string;
  name?: string;
  angle?: string;
  framing?: string;
  quality?: string;
  annotation: HorseAnnotation | null;
}
// The history route enriches the existing observation, retaining its stable id and blob_ids.
export interface NodeObservation {
  comment?: string;
  id: string;
  horse_id?: string;
  observed_at: string;
  observer_id: string;
  location: string;
  blob_ids: string[];
  name?: string;
  photos?: ObservationPhoto[];
}
