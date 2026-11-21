// Go /api/v1 comparison contract. All geometry uses the original image axes.
export type ImagePoint = [number, number];
export interface HorseAnnotation {
  horse_polygon: ImagePoint[];
  traits: { key: string; value: string; polygon: ImagePoint[] | null }[];
}
export interface ComparisonRequest {
  blob_sha256: string;
  top_k: number;
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
    }[];
  }[];
}
export type ComparisonDecision =
  | { action: 'match'; horse_id: string; observation_name: string }
  | { action: 'create_horse'; horse_name: string; observation_name: string };
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
  id: string;
  horse_id?: string;
  observed_at: string;
  observer_id: string;
  location: string;
  blob_ids: string[];
  name?: string;
  photos?: ObservationPhoto[];
}
