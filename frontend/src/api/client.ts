import axios from 'axios'

const http = axios.create({ baseURL: '/api/v1' })

export interface KV { key: string; value: string }
export interface Store {
  id: number; address: string; status_name: string; labels: KV[]
  capacity: string; available: string; region_count: number
}
export interface Cluster {
  id: string
  name: string
  tidb_host: string
  tidb_port: number
  tidb_user: string
  pd_endpoint: string
  prometheus_url?: string
  version?: string
  status: string
}
export interface Tenant {
  id: string; cluster_id: string; name: string; isolation_level: string
  label_key?: string; label_value?: string
  placement_policy?: string; resource_group?: string; ru_per_sec?: number
  priority: string; status: string
}
export interface DryRunResult {
  affected_regions: number; total_size_mb: number; replication_factor: number; needed_mb: number
  target_pool_count: number; target_avail_mb: number; target_capacity_ok: boolean
  est_minutes: number; warnings?: string[]
}

export interface PlacementPolicy {
  policy_id: number; catalog_name: string; policy_name: string
  primary_region: string; regions: string
  constraints: string; leader_constraints: string; follower_constraints: string; learner_constraints: string
  schedule: string; followers: number; learners: number
  survival_preferences: string
}

export interface PlacementLabel {
  key: string; values: string[]
}

export interface CreatePlacementPolicyReq {
  name: string
  primary_region?: string
  regions?: string
  schedule?: string
  followers?: number
  constraints?: string
  leader_constraints?: string
  follower_constraints?: string
  learner_constraints?: string
  learners?: number
  survival_preferences?: string
}
export interface MetricSeries { labels: Record<string, string>; values: [number, number][] }
export interface MetricSummary { qps?: number; p99_s?: number; storage_bytes?: number }
export interface ClusterHealth {
  name: string; tidb_ok: boolean; version?: string; tidb_error?: string
  pd_ok: boolean; pd_error?: string; prom_ok?: boolean; capabilities?: string[]
}
export interface StoreResource {
  store_id: number; memory_limit: string; memory_limit_fmt: string
  memory_current: number; memory_usage_pct: number
  cpu_quota: number; cpu_usage_nsec: number; cpu_usage_pct: number
  read_bandwidth: string; write_bandwidth: string
  block_cache_size: string; resource_control: boolean
}
export interface TenantDatabase {
  name: string; size_mb: number; table_count: number
}
export interface TenantDetail {
  tenant: Tenant
  databases: TenantDatabase[]
  stores: Store[]
  store_match_mode: string         // "label" | "all"
  store_shared_note?: string
  total_store_count: number
}

export interface ResourceGroupDetail {
  name: string; ru_per_sec: number; priority: string; burstable: number
}
export interface TenantResourceDetail {
  resource_group?: ResourceGroupDetail
  stores?: Store[]
  placement_policy?: PlacementPolicy
  isolation_level: string
  store_match_mode: string
}

export interface UpdateTenantReq {
  ru_per_sec?: number
  priority?: string
  burstable?: boolean
}

export interface DBUser {
  user: string; host: string; resource_group?: string
}
export interface PrivilegeEntry {
  grantee: string; table_schema: string; table_name: string
  privilege_type: string; is_grantable: string
}

export interface DatabaseInfo {
  name: string; placement_policy?: string; table_count: number; size_mb: number
}
export interface TableInfo {
  name: string; schema: string; placement_policy?: string
  row_count: number; size_mb: number; engine: string
}

export interface CreateClusterReq {
  name: string
  tidb_host: string
  tidb_port: number
  tidb_user: string
  tidb_password: string
  pd_endpoint?: string
  prometheus_url?: string
  skip_probe?: boolean
}

export const api = {
  listClusters: () => http.get<Cluster[]>('/clusters').then(r => r.data),
  createCluster: (body: CreateClusterReq) => http.post<Cluster>('/clusters', body).then(r => r.data),
  // 注意：cluster id 是 AUTO_RANDOM 大整数（>2^53），全程以字符串传递，绝不可 Number()。
  listStores: (cid: string) => http.get<Store[]>(`/clusters/${cid}/topology/stores`).then(r => r.data),
  setStoreLabel: (cid: string, sid: number, body: KV) =>
    http.put(`/clusters/${cid}/stores/${sid}/labels`, body).then(r => r.data),
  listPlacementPolicies: (cid: string) => http.get<PlacementPolicy[]>(`/clusters/${cid}/placement-policies`).then(r => r.data),
  createPlacementPolicy: (cid: string, body: CreatePlacementPolicyReq) =>
    http.post(`/clusters/${cid}/placement-policies`, body).then(r => r.data),
  alterPlacementPolicy: (cid: string, pname: string, body: CreatePlacementPolicyReq) =>
    http.put(`/clusters/${cid}/placement-policies/${encodeURIComponent(pname)}`, body).then(r => r.data),
  dropPlacementPolicy: (cid: string, pname: string) =>
    http.delete(`/clusters/${cid}/placement-policies/${encodeURIComponent(pname)}`).then(r => r.data),
  listPlacementLabels: (cid: string) => http.get<PlacementLabel[]>(`/clusters/${cid}/placement-labels`).then(r => r.data),
  listResourceGroups: (cid: string) => http.get(`/clusters/${cid}/resource-groups`).then(r => r.data),
  listUsers: (cid: string) => http.get<DBUser[]>(`/clusters/${cid}/users`).then(r => r.data),
  createUser: (cid: string, body: { user: string; host?: string; password: string; resource_group?: string }) =>
    http.post(`/clusters/${cid}/users`, body).then(r => r.data),
  updateUser: (cid: string, username: string, host: string, body: { password?: string; resource_group?: string }) =>
    http.put(`/clusters/${cid}/users/${encodeURIComponent(username)}?host=${encodeURIComponent(host)}`, body).then(r => r.data),
  deleteUser: (cid: string, username: string, host: string) =>
    http.delete(`/clusters/${cid}/users/${encodeURIComponent(username)}?host=${encodeURIComponent(host)}`).then(r => r.data),
  listUserPrivileges: (cid: string, username: string, host: string) =>
    http.get(`/clusters/${cid}/users/${encodeURIComponent(username)}/privileges?host=${encodeURIComponent(host)}`).then(r => r.data),
  grantPrivilege: (cid: string, username: string, host: string, body: { privileges: string; database: string; table?: string }) =>
    http.post(`/clusters/${cid}/users/${encodeURIComponent(username)}/privileges?host=${encodeURIComponent(host)}`, body).then(r => r.data),
  revokePrivilege: (cid: string, username: string, host: string, body: { privileges: string; database: string; table?: string }) =>
    http.delete(`/clusters/${cid}/users/${encodeURIComponent(username)}/privileges?host=${encodeURIComponent(host)}`, { data: body }).then(r => r.data),

  listDatabases: (cid: string) => http.get<DatabaseInfo[]>(`/clusters/${cid}/databases`).then(r => r.data),
  listTables: (cid: string, dbname: string) => http.get<TableInfo[]>(`/clusters/${cid}/databases/${encodeURIComponent(dbname)}/tables`).then(r => r.data),
  bindDatabasePolicy: (cid: string, dbname: string, policy: string) =>
    http.put(`/clusters/${cid}/databases/${encodeURIComponent(dbname)}/policy`, { policy }).then(r => r.data),
  bindTablePolicy: (cid: string, dbname: string, tname: string, policy: string) =>
    http.put(`/clusters/${cid}/databases/${encodeURIComponent(dbname)}/tables/${encodeURIComponent(tname)}/policy`, { policy }).then(r => r.data),
  createResourceGroup: (cid: string, body: { name: string; ru_per_sec?: number; priority?: string; burstable?: boolean }) =>
    http.post(`/clusters/${cid}/resource-groups`, body).then(r => r.data),
  alterResourceGroup: (cid: string, rgname: string, body: { ru_per_sec?: number; priority?: string; burstable?: boolean }) =>
    http.put(`/clusters/${cid}/resource-groups/${encodeURIComponent(rgname)}`, body).then(r => r.data),
  dropResourceGroup: (cid: string, rgname: string) =>
    http.delete(`/clusters/${cid}/resource-groups/${encodeURIComponent(rgname)}`).then(r => r.data),
  dryRunPlacement: (cid: string, body: any) =>
    http.post<DryRunResult>(`/clusters/${cid}/placement-policies/dry-run`, body).then(r => r.data),
  getMetricsRU: (cid: string, from: number, to: number) =>
    http.get<MetricSeries[]>(`/clusters/${cid}/metrics/ru`, { params: { from, to, step: 60 } }).then(r => r.data),
  getMetricsSummary: (cid: string) => http.get<MetricSummary>(`/clusters/${cid}/metrics/summary`).then(r => r.data),
  getClusterHealth: (cid: string) => http.get<ClusterHealth>(`/clusters/${cid}/health`).then(r => r.data),
  getStoreResource: (cid: string, sid: number) =>
    http.get<StoreResource>(`/clusters/${cid}/stores/${sid}/resource`).then(r => r.data),
  listTenants: (cid: string) => http.get<Tenant[]>('/tenants', { params: { cluster_id: cid } }).then(r => r.data),
  // 注意：tenant id 是 AUTO_RANDOM 大整数（>2^53），必须以字符串传入，绝不可 Number() 否则精度丢失→404/500。
  getTenantDetail: (tid: string) => http.get<TenantDetail>(`/tenants/${tid}/detail`).then(r => r.data),
  createTenant: (body: any) => http.post('/tenants', body).then(r => r.data),
  deleteTenant: (tid: string) => http.delete(`/tenants/${tid}`).then(r => r.data),
  updateTenant: (tid: string, body: UpdateTenantReq) => http.put(`/tenants/${tid}`, body).then(r => r.data),
  getTenantResource: (tid: string) => http.get<TenantResourceDetail>(`/tenants/${tid}/resource`).then(r => r.data),
  suspendTenant: (tid: string) => http.post(`/tenants/${tid}/suspend`).then(r => r.data),
  activateTenant: (tid: string) => http.post(`/tenants/${tid}/activate`).then(r => r.data),
}
