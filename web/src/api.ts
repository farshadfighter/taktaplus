// Thin JSON client for the TaktaPlus API. Every state-changing call
// carries the CSRF header the server requires.

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const res = await fetch('/api/v1' + path, {
    method,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      'X-Requested-With': 'taktaplus',
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return undefined as T
  const ct = res.headers.get('Content-Type') ?? ''
  const data = ct.includes('application/json') ? await res.json() : await res.text()
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith('/auth/')) onUnauthorized()
    const msg = typeof data === 'object' && data && 'error' in data ? String(data.error) : res.statusText
    throw new ApiError(res.status, msg)
  }
  return data as T
}

export const api = {
  get: <T>(p: string) => request<T>('GET', p),
  post: <T>(p: string, b?: unknown) => request<T>('POST', p, b ?? {}),
  put: <T>(p: string, b: unknown) => request<T>('PUT', p, b),
  del: <T>(p: string, b?: unknown) => request<T>('DELETE', p, b),
}

// getText calls an endpoint that answers with a file (for downloads).
export async function getText(path: string): Promise<string> {
  const res = await fetch('/api/v1' + path, { credentials: 'same-origin' })
  if (!res.ok) {
    const data = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(res.status, String(data.error ?? res.statusText))
  }
  return res.text()
}

// --- types mirroring the server JSON ---

export type User = {
  id: number
  username: string
  name: string
  email: string
  role: string
  perms: string[]
  totp_enabled: boolean
}

export type UserInfo = {
  id: number
  username: string
  name: string
  email: string
  phone: string
  role: string
  source: string
  totp_enabled: boolean
  active: boolean
  locked: boolean
  last_login_at: string | null
  created_at: string
}

export type LicStatus = 'active' | 'grace' | 'expired' | 'unlicensed'

export type Peer = { serial: string; license: LicStatus }

export type Device = {
  id: number
  name: string
  kind: 'fortigate' | 'fortiweb'
  host: string
  https_port: number
  ssh_port: number
  transport_order: string
  group_id: number | null
  group_name: string
  tags: string[]
  tls_pin: string
  ssh_host_key: string
  has_api: boolean
  has_ssh: boolean
  serial: string
  model: string
  firmware: string
  build: string
  hostname: string
  ha_mode: string
  ha_peers: Peer[]
  vdoms: string[]
  fortimanager: boolean
  status: 'unknown' | 'online' | 'offline' | 'error'
  last_error: string
  last_seen_at: string | null
  last_probe_at: string | null
  created_at: string
  license: LicStatus
}

export type Iface = { name: string; alias: string; ip: string; up: boolean; speed: number; rx_bytes: number; tx_bytes: number }
export type Signature = { name: string; version: string }
export type Contract = { name: string; status: string; expires: string | null }

export type DeviceStatus = {
  at: string
  cpu: number
  mem: number
  disk: number
  sessions: number
  uptime: number
  interfaces: Iface[] | null
  signatures: Signature[] | null
  contracts: Contract[] | null
  signatures_at: string | null
}

export type DeviceRow = Device & { health: DeviceStatus | null }

export type Metric = { at: string; cpu: number; mem: number; disk: number; sessions: number }

export type Group = { id: number; name: string; description: string; devices: number }

export type Credentials = {
  api_token?: string
  api_user?: string
  api_pass?: string
  ssh_user?: string
  ssh_password?: string
  ssh_key?: string
}

export type DeviceInput = {
  name: string
  kind: string
  host: string
  https_port: number
  ssh_port: number
  transport_order: string
  group_id: number | null
  tags: string[]
  credentials?: Credentials
  tls_pin: string
  ssh_host_key: string
}

export type Discovery = {
  info: { serial: string; model: string; firmware: string; build: string; hostname: string; ha_mode: string; ha_peers: string[] | null; vdoms: string[] | null; fortimanager: boolean }
  tls_pin: string
  ssh_host_key: string
  license: LicStatus
  ha_peers: Peer[]
}

export type AlertRow = {
  id: number
  rule_id: number
  rule_name: string
  kind: string
  device_id: number
  device_name: string
  subject: string
  severity: 'info' | 'warning' | 'critical'
  message: string
  state: 'open' | 'resolved'
  opened_at: string
  resolved_at: string | null
  acked_by: string
  acked_at: string | null
}

export type Rule = {
  id: number
  name: string
  kind: string
  threshold: number
  duration_minutes: number
  severity: string
  group_id: number | null
  channel_ids: number[]
  repeat_minutes: number
  enabled: boolean
}

export type ChannelConfig = {
  smtp_host?: string
  smtp_port?: number
  smtp_security?: string
  smtp_user?: string
  smtp_password?: string
  from?: string
  to?: string[]
  url?: string
  secret?: string
  address?: string
  protocol?: string
}

export type Channel = { id: number; name: string; kind: 'email' | 'webhook' | 'syslog'; config: ChannelConfig; enabled: boolean }

export type Backup = {
  id: number
  device_id: number
  device_name: string
  at: string
  reason: 'scheduled' | 'manual' | 'pre_restore' | 'on_change'
  actor: string
  status: 'ok' | 'failed'
  error: string
  sha256: string
  size: number
  firmware: string
  locked: boolean
  note: string
}

export type ConfigChange = { path: string; kind: 'added' | 'removed' | 'changed'; key: string; old?: string; new?: string }

export type Policy = {
  id: number
  name: string
  group_id: number | null
  device_id: number | null
  schedule: string
  keep_count: number
  keep_days: number
  enabled: boolean
  last_run_at: string | null
  next_run_at: string | null
}

export type RestoreJob = {
  id: number
  device_id: number
  device_name: string
  backup_id: number
  pre_backup_id: number | null
  actor: string
  status: 'running' | 'waiting' | 'succeeded' | 'failed'
  error: string
  started_at: string
  finished_at: string | null
}

export type LicenseItem = { kind: 'device' | 'otp'; serial?: string; platform?: string; users?: number; expires: string; status: LicStatus }

export type LicenseInfo = {
  installation_id: string
  online_configured: boolean
  clock_rolled_back: boolean
  state: { installed_at: string | null; last_heartbeat_at: string | null; last_heartbeat_status: string; last_error: string }
  license: null | {
    license_id: string
    customer: { id: string; name: string }
    issued_at: string
    not_after: string
    grace_days: number
    online: boolean
    items: LicenseItem[]
    otp_capacity: number
    kid: string
  }
}

export type AuditEntry = {
  id: number
  at: string
  source: string
  actor: string
  ip: string
  action: string
  object_type: string
  object_id: string
  details: Record<string, unknown>
}

export type RepoPackage = {
  id: number
  sha256: string
  file: string
  size: number
  platform: 'fortigate' | 'fortiweb'
  type: string
  version: string
  models: string[]
  fw_min: string
  fw_max: string
  upgrade_from: string[]
  notes: string
  released_at: string
  source_id: number | null
  origin: string
  verified: boolean
  withdrawn: boolean
  created_at: string
}

export type SourceConfig = {
  url?: string
  host?: string
  port?: number
  user?: string
  password?: string
  dir?: string
  tls?: boolean
  host_key?: string
  files?: string[]
}

export type PackageSource = {
  id: number
  name: string
  kind: 'central' | 'http' | 'ftp' | 'sftp' | 'tftp'
  config: SourceConfig
  schedule: string
  enabled: boolean
  last_sync_at: string | null
  last_status: string
  last_error: string
  next_sync_at: string | null
}

export type SyncRun = {
  id: number
  source_id: number | null
  origin: string
  started_at: string
  finished_at: string | null
  status: 'running' | 'ok' | 'failed'
  added: number
  seen: number
  error: string
  actor: string
}

export type ChangeStatus = 'draft' | 'pending' | 'approved' | 'running' | 'succeeded' | 'failed' | 'partial' | 'cancelled' | 'rejected'

export type Change = {
  id: number
  kind: 'signatures' | 'firmware' | 'script'
  title: string
  description: string
  spec: Record<string, unknown>
  status: ChangeStatus
  run_mode: 'window' | 'at' | 'now'
  run_at: string | null
  urgent_reason: string
  concurrency: number
  canary: number
  max_failures: number
  backup_first: boolean
  required_approvals: number
  created_by: number | null
  creator: string
  created_at: string
  submitted_at: string | null
  started_at: string | null
  finished_at: string | null
  targets: number
  succeeded_targets: number
  failed_targets: number
  approvals: number
  next_run_at: string | null
}

export type ChangeTarget = {
  id: number
  device_id: number
  device_name: string
  position: number
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'skipped'
  preview: string
  output: string
  error: string
  pre_backup_id: number | null
  started_at: string | null
  finished_at: string | null
}

export type ChangeApproval = { username: string; decision: 'approve' | 'reject'; comment: string; at: string }

export type MaintWindow = { id: number; name: string; group_id: number | null; weekdays: number[]; start_time: string; minutes: number; enabled: boolean }
export type ApprovalPolicy = { id: number; group_id: number | null; required: number }

export type Script = { id: number; name: string; description: string; platform: string; body: string; read_only: boolean; updated_by: string; updated_at: string }
export type Variable = { id: number; scope: 'global' | 'group' | 'device'; scope_id: number; name: string; value: string }
export type ScriptRun = { id: number; script_id: number | null; device_id: number; device_name: string; at: string; actor: string; script: string; output: string; ok: boolean; error: string }
