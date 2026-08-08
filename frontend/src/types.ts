// Types mirroring the state.json schema (schema_version 1).
// The frontend is fully data-driven: it knows only these shapes, never
// individual service names, ports, or units.

export type Status = 'healthy' | 'warning' | 'failed' | 'unknown' | 'stale'

export interface HostState {
  hostname: string
  uptime_seconds: number
  cpu: { cores: number; percent: number }
  memory: { total_bytes: number; used_bytes: number; percent: number }
  swap: { total_bytes: number; used_bytes: number; percent: number }
  disk: { mount_point: string; total_bytes: number; used_bytes: number; percent: number }
  load: { load1: number; load5: number; load15: number }
}

export interface MemoryInfo {
  rss_bytes: number
  source: 'cgroup_memory_current' | 'proc_rss'
}

export interface Listener {
  protocol: 'tcp' | 'udp'
  port: number
  address: string
  internal: boolean
  pid?: number
  process?: string
  exposure?: string
}

export interface HealthInfo {
  problems?: string[]
  details?: string[]
}

export interface Service {
  id: string
  name: string
  status: Status
  unit?: string
  unit_state?: string
  version?: string
  memory?: MemoryInfo
  config_path?: string
  config_exists?: boolean
  restart_enabled?: boolean
  listeners?: Listener[]
  health?: HealthInfo
}

export interface Health {
  status: Status
  stale: boolean
  age_seconds: number
  services_healthy: number
  services_warning: number
  services_failed: number
  services_unknown: number
  message?: string
}

export type NodeType = 'internet' | 'port' | 'protocol' | 'service'

// Data carried by a React Flow node. Extends Record<string, unknown> because
// @xyflow/react requires node data to satisfy that constraint.
export interface TopoNode extends Record<string, unknown> {
  id: string
  type: NodeType
  label: string
  service_id?: string
  protocol?: string
  port?: number
  port_start?: number
  port_end?: number
  target_port?: number
  exposure?: string
  status?: Status
}

export interface Evidence {
  source: string
  confidence: string
}

// Data carried by a React Flow edge.
export interface TopoEdge extends Record<string, unknown> {
  id: string
  source: string
  target: string
  evidence?: Evidence
}

export interface Topology {
  nodes: TopoNode[]
  edges: TopoEdge[]
}

export interface State {
  schema_version: number
  generated_at: string
  collector_version: string
  collect_duration_ms: number
  host: HostState
  services: Service[]
  health: Health
  topology: Topology
}

export interface Healthz {
  status: string
}

export function formatBytes(bytes: number): string {
  if (bytes == null || Number.isNaN(bytes) || bytes < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = bytes
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  if (v >= 100) return `${Math.round(v)} ${units[i]}`
  if (v >= 10) return `${v.toFixed(1)} ${units[i]}`
  return `${v.toFixed(2)} ${units[i]}`
}

export function formatDuration(seconds: number): string {
  if (seconds == null || seconds < 0) return '—'
  const d = Math.floor(seconds)
  const days = Math.floor(d / 86400)
  const hours = Math.floor((d % 86400) / 3600)
  const mins = Math.floor((d % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m ${d % 60}s`
}

export function formatAge(seconds: number): string {
  if (seconds == null) return '—'
  if (seconds < 60) return `${Math.round(seconds)}s ago`
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`
  return `${(seconds / 3600).toFixed(1)}h ago`
}

/** Service list sort: failed > warning > unknown > healthy, RAM descending within a status. */
const STATUS_ORDER: Record<Status, number> = {
  failed: 0,
  warning: 1,
  unknown: 2,
  healthy: 3,
  stale: 0,
}

export function sortServices(services: Service[]): Service[] {
  return [...services].sort((a, b) => {
    const diff = STATUS_ORDER[a.status] - STATUS_ORDER[b.status]
    if (diff !== 0) return diff
    return (b.memory?.rss_bytes ?? 0) - (a.memory?.rss_bytes ?? 0)
  })
}

/** Build a collapsible tree from the flat topology for the mobile view. */
export interface TreePort {
  port: number
  portStart: number
  portEnd: number
  /** Rendered label: single port or "start–end". */
  label: string
  protocols: TreeProtocol[]
}

export interface TreeProtocol {
  protocol: 'tcp' | 'udp'
  services: TreeService[]
}

export interface TreeService {
  serviceId: string
  name: string
  status?: Status
  instanceId: string
}

/** Format a port range: single port when start==end, else "start–end". */
export function formatPortRange(start: number, end: number): string {
  if (start === end) return `${start}`
  return `${start}–${end}`
}

export function buildPortTree(topology: Topology): TreePort[] {
  const ports = new Map<string, TreePort>()

  for (const node of topology.nodes) {
    if (node.type !== 'service') continue
    // Service nodes carry the backend port; the enclosing port/protocol nodes
    // carry the range. Key the tree on the parent port node's range.
    const startRaw = node.port_start ?? node.port
    if (startRaw == null) continue
    const start: number = startRaw
    const end: number = node.port_end ?? start

    const key = `${start}:${end}`
    let port = ports.get(key)
    if (!port) {
      port = { port: start, portStart: start, portEnd: end, label: formatPortRange(start, end), protocols: [] }
      ports.set(key, port)
    }
    const proto = (node.protocol === 'udp' ? 'udp' : 'tcp') as 'tcp' | 'udp'
    let tp = port.protocols.find((p) => p.protocol === proto)
    if (!tp) {
      tp = { protocol: proto, services: [] }
      port.protocols.push(tp)
    }
    tp.services.push({
      serviceId: node.service_id ?? node.id,
      name: node.label,
      status: node.status,
      instanceId: node.id,
    })
  }

  const tree = [...ports.values()].sort((a, b) => a.portStart - b.portStart || a.portEnd - b.portEnd)
  for (const p of tree) {
    p.protocols.sort((a, b) => (a.protocol === b.protocol ? 0 : a.protocol === 'tcp' ? -1 : 1))
    for (const pr of p.protocols) {
      pr.services.sort((a, b) => a.name.localeCompare(b.name))
    }
  }
  return tree
}

export function findServiceNode(topology: Topology, serviceId: string): TopoNode | undefined {
  return topology.nodes.find((n) => n.type === 'service' && n.service_id === serviceId)
}

// Schema versions the frontend understands.
export const SCHEMA_VERSION = 1

// SchemaError describes why a state payload could not be used.
export class SchemaError extends Error {
  kind: 'schema' | 'malformed'
  constructor(kind: 'schema' | 'malformed', message: string) {
    super(message)
    this.kind = kind
  }
}

/**
 * Validates a parsed state payload before the UI renders it. Returns the state
 * or throws SchemaError (schema mismatch / malformed). The UI shows a clear but
 * minimal message instead of white-screening.
 */
export function validateState(payload: unknown): State {
  if (payload == null || typeof payload !== 'object') {
    throw new SchemaError('malformed', 'State is not an object')
  }
  const s = payload as Partial<State>
  if (s.schema_version == null) {
    throw new SchemaError('malformed', 'State has no schema_version')
  }
  if (s.schema_version !== SCHEMA_VERSION) {
    throw new SchemaError('schema', `Unsupported schema version ${s.schema_version} (expected ${SCHEMA_VERSION})`)
  }
  if (!s.host || !Array.isArray(s.services) || !s.health || !s.topology || !Array.isArray(s.topology.nodes) || !Array.isArray(s.topology.edges)) {
    throw new SchemaError('malformed', 'State is missing required sections')
  }
  return s as State
}
