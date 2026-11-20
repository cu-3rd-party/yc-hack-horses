import type {
  Blob,
  Horse,
  HorseUpsertPayload,
  NodeStatus,
  Observation,
  ObservationCreatePayload,
  ObservationsListResponse,
  HorsesListResponse,
} from '@yc-hack-horses/contracts';
import { ApiClient } from './client';
import { NODE_API_BASE } from '../config/node';

export const api = new ApiClient(NODE_API_BASE);

export type { ApiError, NodeUnavailableError } from './client';

// --- horses ---

export function getHorses(query?: { limit?: number; offset?: number }) {
  return api.get<HorsesListResponse>('/api/v1/horses', { query, timeoutMs: 8000 });
}

export function getHorse(id: string) {
  return api.get<Horse>(`/api/v1/horses/${encodeURIComponent(id)}`, { timeoutMs: 8000 });
}

export function createHorse(payload: HorseUpsertPayload) {
  return api.post<Horse>('/api/v1/horses', payload, { timeoutMs: 8000 });
}

export function patchHorse(id: string, payload: HorseUpsertPayload) {
  return api.patch<Horse>(`/api/v1/horses/${encodeURIComponent(id)}`, payload, {
    timeoutMs: 8000,
  });
}

// --- observations ---

export function getObservations(query?: { horse_id?: string; limit?: number; offset?: number }) {
  return api.get<ObservationsListResponse>('/api/v1/observations', { query, timeoutMs: 8000 });
}

export function createObservation(payload: ObservationCreatePayload) {
  return api.post<Observation>('/api/v1/observations', payload, { timeoutMs: 8000 });
}

// --- node ---

export function getNodeStatus() {
  return api.get<NodeStatus>('/api/v1/node', { timeoutMs: 3000 });
}

// --- blobs ---

export function uploadBlob(file: File) {
  return api.uploadBlob(file);
}

export function getBlob(id: string) {
  return api.get<Blob>(`/api/v1/blobs/${encodeURIComponent(id)}`, { timeoutMs: 8000 });
}
