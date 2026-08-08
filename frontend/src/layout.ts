import dagre from '@dagrejs/dagre'
import type { Node, Edge } from '@xyflow/react'
import type { TopoNode, TopoEdge } from '@/types'

// Node sizes per type.
export const NODE_W = {
  internet: 140,
  port: 150,
  protocol: 96,
  service: 180,
} as const

export const NODE_H = 48

export interface LayoutResult {
  nodes: Node<TopoNode>[]
  edges: Edge<TopoEdge>[]
}

/**
 * Applies Dagre hierarchical top-to-bottom layout to the port tree.
 * Adapted from Homelable's applyDagreLayout. The tree is deterministic:
 * same input → same positions.
 */
export function applyDagreLayout(topologyNodes: TopoNode[], topologyEdges: TopoEdge[]): LayoutResult {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'TB', nodesep: 48, ranksep: 64 })

  const nodes = topologyNodes.map((n) => {
    const w = NODE_W[n.type] ?? 160
    const h = NODE_H
    g.setNode(n.id, { width: w, height: h })
    return { id: n.id, type: n.type, data: n, position: { x: 0, y: 0 }, width: w, height: h }
  })

  const edges: Edge<TopoEdge>[] = topologyEdges.map((e, i) => ({
    id: e.id || `e-${i}`,
    source: e.source,
    target: e.target,
    data: e,
    type: 'smoothstep',
    animated: false,
  }))

  for (const e of topologyEdges) {
    g.setEdge(e.source, e.target)
  }

  dagre.layout(g)

  for (const node of nodes) {
    const pos = g.node(node.id)
    const w = NODE_W[node.data.type] ?? 160
    const h = NODE_H
    node.position = { x: pos.x - w / 2, y: pos.y - h / 2 }
    node.width = w
    node.height = h
  }

  return { nodes, edges }
}
