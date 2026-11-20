// Единый источник API-контрактов yc-hack-horses.
// Соответствует REST/WS-контракту Go-ноды: /api/v1/*, WS /api/v1/events.

export type ISO8601 = string;

export type IdentityStatus = 'known' | 'unknown' | 'provisional';

export type NodeRole = 'local' | 'central' | 'unknown';

export interface Horse {
  id: string;
  created_at: ISO8601;
  identity_status: IdentityStatus;
  metadata: HorseMetadata;
}

export interface HorseMetadata {
  name?: string;
  nickname?: string;
  sex?: 'male' | 'female' | 'unknown';
  birth_year?: number | null;
  color?: string;
  notes?: string;
  [key: string]: unknown;
}

export interface GeoLocation {
  lat: number;
  lon: number;
  accuracy_m?: number | null;
}

export interface Observation {
  id: string;
  horse_id?: string | null;
  node_id: string;
  observed_at: ISO8601;
  location?: GeoLocation | null;
  observer_id: string;
  blob_ids: string[];
  trait_snapshot_id?: string | null;
}

export interface TraitSnapshot {
  id: string;
  observation_id: string;
  model_version: string;
  traits: TraitValue[];
  confidence: number;
  fingerprint?: string | null;
  confirmed: boolean;
  edited: boolean;
}

export type TraitValue = {
  trait: string;
  value: string;
  confidence?: number | null;
};

export interface Blob {
  id: string;
  sha256: string;
  mime_type: string;
  size: number;
}

// --- REST ответы ---

export interface HorsesListResponse {
  horses: Horse[];
  total?: number;
}

export interface ObservationsListResponse {
  observations: Observation[];
  total?: number;
}

export interface HorseUpsertPayload {
  id?: string;
  identity_status?: IdentityStatus;
  metadata?: HorseMetadata;
}

export interface ObservationCreatePayload {
  horse_id?: string | null;
  observed_at?: ISO8601;
  location?: GeoLocation | null;
  observer_id?: string;
  blob_ids?: string[];
  trait_snapshot?: {
    model_version: string;
    traits: TraitValue[];
    confidence: number;
    fingerprint?: string | null;
  };
}

// --- Статус ноды / синхронизации ---

export interface NodeStatus {
  node_id: string;
  role: NodeRole;
  version?: string;
  uptime_s?: number;
  storage?: NodeStorageStatus;
  certificate?: CertificateStatus;
  peers?: PeerStatus[];
}

export interface NodeStorageStatus {
  engine: 'sqlite' | 'postgres';
  size_bytes?: number;
  pending_events?: number;
  applied_events?: number;
  errors?: number;
}

export interface CertificateStatus {
  valid: boolean;
  subject?: string;
  expires_at?: ISO8601 | null;
  trust_anchor_ok?: boolean;
}

export interface PeerStatus {
  peer_id: string;
  state: 'connected' | 'disconnected' | 'error' | 'syncing';
  last_sync_at?: ISO8601 | null;
  events_sent?: number;
  events_received?: number;
  last_error?: string | null;
}

// --- WS /api/v1/events ---

export type WsEvent =
  | { type: 'observation.created'; payload: Observation }
  | { type: 'horse.created'; payload: Horse }
  | { type: 'horse.updated'; payload: Horse }
  | { type: 'trait_snapshot.created'; payload: TraitSnapshot }
  | { type: 'blob.stored'; payload: Blob }
  | { type: 'sync.peer_state'; payload: PeerStatus }
  | { type: 'node.status'; payload: NodeStatus }
  | { type: string; payload?: unknown };
