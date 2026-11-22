export interface FieldErrorItem {
  field: string;
  message: string;
}

export interface ProblemDetails {
  type?: string;
  title?: string;
  status: number;
  detail?: string;
  code?: string;
  request_id?: string;
  errors?: FieldErrorItem[];
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

export interface MetricsSummary {
  org_id: string;
  range: string;
  window_seconds: number;
  total_requests: number;
  rps: number;
  errors_4xx: number;
  errors_5xx: number;
  auth_denied: number;
  rate_limited: number;
  error_rate: number;
  avg_latency_ms: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
  p99_latency_ms: number;
  active_gateways: number;
  stale_gateways: number;
  data_available: boolean;
}

export interface TimeSeriesPoint {
  timestamp: string;
  requests: number;
  errors_4xx: number;
  errors_5xx: number;
  auth_denied: number;
  rate_limited: number;
  avg_latency_ms: number;
  p95_latency_ms: number;
}

export interface TimeSeriesResponse {
  org_id: string;
  range: string;
  bucket_minutes: number;
  points: TimeSeriesPoint[];
  data_available: boolean;
}

export interface ErrorBreakdown {
  org_id: string;
  range: string;
  total_requests: number;
  total_errors: number;
  by_status_class: Record<string, number>;
  by_outcome: Record<string, number>;
  data_available: boolean;
}

export interface GatewayInstanceStatus {
  instance_id: string;
  org_id: string;
  active_version: number;
  checksum: string;
  ready: boolean;
  build_version?: string;
  last_seen_at: string;
  status: "healthy" | "stale" | "offline";
  converged: boolean;
  age_sec: number;
}

export interface AuditEventItem {
  id: string;
  org_id?: string;
  actor_user_id?: string;
  action: string;
  resource_type: string;
  resource_id?: string;
  request_id?: string;
  outcome: string;
  details?: Record<string, unknown>;
  created_at: string;
}

export interface ServiceItem {
  id: string;
  org_id: string;
  slug: string;
  name: string;
  description: string;
  enabled: boolean;
  revision: number;
  created_at: string;
  updated_at: string;
}

export interface UpstreamTargetItem {
  id: string;
  pool_id: string;
  url: string;
  weight: number;
  enabled: boolean;
}

export interface UpstreamPoolItem {
  id: string;
  org_id: string;
  name: string;
  destination_class: string;
  connect_timeout_ms: number;
  read_timeout_ms: number;
  revision: number;
  targets?: UpstreamTargetItem[];
}

export interface RouteItem {
  id: string;
  org_id: string;
  service_id: string;
  upstream_pool_id: string;
  name: string;
  host: string;
  path_prefix: string;
  methods: string[];
  strip_prefix: boolean;
  priority: number;
  enabled: boolean;
  revision: number;
}

export interface ConsumerItem {
  id: string;
  org_id: string;
  name: string;
  description: string;
  owner_email: string;
  status: "active" | "suspended";
  revision: number;
  created_at: string;
}

export interface APIKeyMetadata {
  id: string;
  consumer_id: string;
  key_prefix: string;
  label: string;
  scopes: string[];
  expires_at?: string | null;
  revoked_at?: string | null;
  created_at: string;
}

export interface IssuedAPIKey extends APIKeyMetadata {
  api_key: string;
}

export interface PolicyAttachment {
  id: string;
  policy_id: string;
  scope_type: "org" | "service" | "route";
  service_id?: string | null;
  route_id?: string | null;
  priority: number;
}

export interface PolicyItem {
  id: string;
  org_id: string;
  name: string;
  policy_type: string;
  schema_version: number;
  config: Record<string, unknown>;
  enabled: boolean;
  revision: number;
  attachments?: PolicyAttachment[];
}

export interface PublicationItem {
  id: string;
  org_id: string;
  version: number;
  status: string;
  snapshot_checksum: string;
  notes: string;
  published_at: string;
}

export interface ValidationIssue {
  code: string;
  severity: "error" | "warning";
  resource: string;
  message: string;
}

export interface ValidationReport {
  valid: boolean;
  draft_revision: number;
  errors: ValidationIssue[];
  warnings: ValidationIssue[];
}
