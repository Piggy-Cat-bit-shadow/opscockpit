// Topology subgraph helpers — data-driven, no hardcoded service names or ports.
// The board and focus views derive everything from the flat topology.

import type { Status, TopoNode, TopoEdge, Topology } from '@/types'
import { formatPortRange } from '@/types'

// ---- Ingress Board ----

export interface BoardProtocol {
  protocol: 'tcp' | 'udp'
  /** Chain of service names from the top-level service down through deps. */
  services: string[]
  /** Service ids (for selection/entrypoint lookups). */
  serviceIds: string[]
  status?: Status
  /** True when the port is a NAT ingress (range typically). */
  natTarget?: number
}

export interface BoardCard {
  portStart: number
  portEnd: number
  label: string
  exposure?: string
  protocols: BoardProtocol[]
}

function protoOf(n: TopoNode): 'tcp' | 'udp' {
  return n.protocol === 'udp' ? 'udp' : 'tcp'
}

function nodeById(topo: Topology): Map<string, TopoNode> {
  return new Map(topo.nodes.map((n) => [n.id, n]))
}

function outgoingEdges(topo: Topology, nodeId: string): TopoEdge[] {
  return topo.edges.filter((e) => e.source === nodeId)
}

/** Trace a service node's downstream dependency chain (service → service). */
function serviceChain(topo: Topology, byId: Map<string, TopoNode>, startId: string): string[] {
  const chain: string[] = []
  const seen = new Set<string>()
  let cur = startId
  while (cur && !seen.has(cur)) {
    seen.add(cur)
    const node = byId.get(cur)
    if (!node || node.type !== 'service') break
    if (chain.length === 0 || chain[chain.length - 1] !== node.label) {
      chain.push(node.label)
    }
    // Find the next service node reachable via a service→service edge.
    const next = outgoingEdges(topo, cur)
      .map((e) => byId.get(e.target))
      .find((n) => n && n.type === 'service')
    if (!next) break
    cur = next.id
  }
  return chain
}

/**
 * Build the Ingress Board: one card per public port/range, grouped by
 * protocol, with the service chain and NAT target. Deterministic ordering:
 * single ports and ranges both sort by (start, end) ascending.
 */
export function buildIngressBoard(topo: Topology): BoardCard[] {
  const byId = nodeById(topo)
  const cards = new Map<string, BoardCard>()
  const order: string[] = []

  for (const n of topo.nodes) {
    if (n.type !== 'service') continue
    // Dependency-only service nodes (no port fields) belong to their parent's
    // card; they never create their own port card.
    const startRaw = n.port_start ?? n.port
    if (startRaw == null) continue
    const start: number = startRaw
    const end: number = n.port_end ?? start
    const key = `${start}:${end}`
    let card = cards.get(key)
    if (!card) {
      card = { portStart: start, portEnd: end, label: formatPortRange(start, end), protocols: [] }
      cards.set(key, card)
      order.push(key)
    }
    // Find the protocol node attached to this service instance.
    const protoNode = topo.nodes.find(
      (pn) => pn.type === 'protocol' && pn.protocol === n.protocol && (pn.port_start ?? pn.port) === card.portStart,
    )
    if (!protoNode) continue
    const proto = protoOf(n)
    let bp = card.protocols.find((p) => p.protocol === proto)
    if (!bp) {
      bp = { protocol: proto, services: [], serviceIds: [] }
      card.protocols.push(bp)
    }
    const chain = serviceChain(topo, byId, n.id)
    // Merge chains (a chain may reach the same names via different routes).
    for (const name of chain) {
      if (!bp.services.includes(name)) bp.services.push(name)
    }
    if (n.service_id && !bp.serviceIds.includes(n.service_id)) bp.serviceIds.push(n.service_id)
    if (n.status) bp.status = n.status
    if (n.exposure === 'nat_ingress' || n.target_port) {
      bp.natTarget = n.target_port
    }
  }

  // Deterministic sort: (start, end) ascending.
  const sorted = order
    .map((k) => cards.get(k)!)
    .sort((a, b) => a.portStart - b.portStart || a.portEnd - b.portEnd)
  for (const c of sorted) {
    c.protocols.sort((a, b) => (a.protocol === b.protocol ? 0 : a.protocol === 'tcp' ? -1 : 1))
  }
  return sorted
}

// ---- Focus subgraphs ----

export interface FocusNode extends Record<string, unknown> {
  id: string
  type: 'port' | 'protocol' | 'service'
  label: string
  protocol?: string
  portStart?: number
  portEnd?: number
  targetPort?: number
  serviceId?: string
  status?: Status
  exposure?: string
}

export interface FocusEdge extends Record<string, unknown> {
  id: string
  source: string
  target: string
}

export interface FocusGraph {
  nodes: FocusNode[]
  edges: FocusEdge[]
}

/**
 * Port Focus: the subgraph reachable from one public port node, walking
 * port → protocol → service → downstream dependencies. Nothing unrelated is
 * included. Ordered left→right for the React Flow canvas.
 */
export function buildPortFocus(topo: Topology, portStart: number, portEnd = portStart): FocusGraph {
  const byId = nodeById(topo)
  const g: FocusGraph = { nodes: [], edges: [] }
  const seen = new Set<string>()

  const add = (n: TopoNode): FocusNode | undefined => {
    if (seen.has(n.id)) return undefined
    seen.add(n.id)
    const f: FocusNode = {
      id: n.id,
      type: n.type as FocusNode['type'],
      label: n.label,
      protocol: n.protocol,
      portStart: n.port_start ?? n.port,
      portEnd: n.port_end ?? n.port_start ?? n.port,
      targetPort: n.target_port,
      serviceId: n.service_id,
      status: n.status,
      exposure: n.exposure,
    }
    g.nodes.push(f)
    return f
  }
  const link = (a: TopoNode, b: TopoNode) => {
    g.edges.push({ id: `${a.id}→${b.id}`, source: a.id, target: b.id })
  }

  // Root port node.
  const port = topo.nodes.find((n) => n.type === 'port' && (n.port_start ?? n.port) === portStart && (n.port_end ?? n.port_start ?? n.port) === portEnd)
  if (!port) return g
  const portFn = add(port)
  if (!portFn) return g

  // Port → protocols.
  for (const pe of outgoingEdges(topo, port.id)) {
    const protoNode = byId.get(pe.target)
    if (!protoNode || protoNode.type !== 'protocol') continue
    const protoFn = add(protoNode)
    if (protoFn) link(port, protoNode)
    // Protocol → services.
    for (const se of outgoingEdges(topo, protoNode.id)) {
      const svcNode = byId.get(se.target)
      if (!svcNode || svcNode.type !== 'service') continue
      const svcFn = add(svcNode)
      if (svcFn) link(protoNode, svcNode)
      // Service → downstream deps.
      for (const de of outgoingEdges(topo, svcNode.id)) {
        const depNode = byId.get(de.target)
        if (!depNode || depNode.type !== 'service') continue
        const depFn = add(depNode)
        if (depFn) link(svcNode, depNode)
      }
    }
  }
  return g
}

/**
 * Service Focus: the reverse graph — every public entrypoint that reaches this
 * service (upstream), plus the service's direct downstream dependencies.
 */
export function buildServiceFocus(topo: Topology, serviceId: string): FocusGraph {
  const byId = nodeById(topo)
  const g: FocusGraph = { nodes: [], edges: [] }
  const seen = new Set<string>()

  const add = (n: TopoNode): FocusNode | undefined => {
    if (seen.has(n.id)) return undefined
    seen.add(n.id)
    g.nodes.push({
      id: n.id,
      type: n.type as FocusNode['type'],
      label: n.label,
      protocol: n.protocol,
      portStart: n.port_start ?? n.port,
      portEnd: n.port_end ?? n.port_start ?? n.port,
      targetPort: n.target_port,
      serviceId: n.service_id,
      status: n.status,
      exposure: n.exposure,
    })
    return g.nodes[g.nodes.length - 1]
  }
  const link = (a: TopoNode, b: TopoNode) => {
    g.edges.push({ id: `${a.id}→${b.id}`, source: a.id, target: b.id })
  }

  // The service's own instance node(s).
  const serviceNodes = topo.nodes.filter((n) => n.type === 'service' && n.service_id === serviceId)
  if (serviceNodes.length === 0) return g
  for (const sn of serviceNodes) add(sn)

  // Upstream: walk edges backwards from each service instance toward the port,
  // traversing service→service dependency edges too (so a dep target like Xray
  // finds the public entrypoint that reaches it via Nginx). Bounded: only
  // nodes on the reverse path are added, never the whole graph.
  const walkUp = (nodeId: string) => {
    for (const e of topo.edges) {
      if (e.target !== nodeId) continue
      const up = byId.get(e.source)
      if (!up) continue
      if (up.type === 'protocol') {
        add(up)
        link(up, byId.get(nodeId)!)
        for (const pe of topo.edges) {
          if (pe.target === up.id) {
            const portNode = byId.get(pe.source)
            if (portNode && portNode.type === 'port') {
              add(portNode)
              link(portNode, up)
            }
          }
        }
      } else if (up.type === 'service') {
        // Recurse through an upstream service (dependency → parent).
        if (!seen.has(up.id)) {
          add(up)
          link(up, byId.get(nodeId)!)
          walkUp(up.id)
        }
      }
    }
  }
  for (const sn of serviceNodes) walkUp(sn.id)

  // Downstream dependencies.
  for (const sn of serviceNodes) {
    for (const de of outgoingEdges(topo, sn.id)) {
      const depNode = byId.get(de.target)
      if (depNode && depNode.type === 'service') {
        add(depNode)
        link(sn, depNode)
      }
    }
  }

  // Deterministic ordering: ports first (ascending), then protocol, then
  // service — stable for layout.
  g.nodes.sort((a, b) => {
    const rank = (x: FocusNode) => (x.type === 'port' ? 0 : x.type === 'protocol' ? 1 : 2)
    const r = rank(a) - rank(b)
    if (r !== 0) return r
    if (a.type === 'port' && b.type === 'port') return (a.portStart ?? 0) - (b.portStart ?? 0)
    return a.label.localeCompare(b.label)
  })
  return g
}
