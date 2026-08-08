import dagre from '@dagrejs/dagre'
import type { Node, Edge } from '@xyflow/react'
import type { FocusGraph, FocusNode, FocusEdge } from '@/topology'

// Node sizes per type. Focus nodes are deliberately large enough to read.
export const NODE_W = {
  port: 180,
  protocol: 96,
  service: 220,
} as const

export const NODE_H = {
  port: 64,
  protocol: 36,
  service: 56,
} as const

export interface LayoutResult {
  nodes: Node<FocusNode>[]
  edges: Edge<FocusEdge>[]
}

/**
 * Left→right Dagre layout for a FOCUS subgraph (small node set). Direction is
 * LR so a port sits on the left and its services/deps flow right. Deterministic:
 * same input → same positions. No fitView of the full 48-node tree.
 */
export function applyFocusLayout(graph: FocusGraph): LayoutResult {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 40, ranksep: 80 })

  const nodes: Node<FocusNode>[] = graph.nodes.map((n) => {
    const w = NODE_W[n.type] ?? 180
    const h = NODE_H[n.type] ?? 56
    g.setNode(n.id, { width: w, height: h })
    return { id: n.id, type: n.type, data: n, position: { x: 0, y: 0 }, width: w, height: h }
  })

  const edges: Edge<FocusEdge>[] = graph.edges.map((e, i) => ({
    id: e.id || `e-${i}`,
    source: e.source,
    target: e.target,
    data: e,
    type: 'smoothstep',
    animated: false,
  }))

  for (const e of graph.edges) {
    g.setEdge(e.source, e.target)
  }

  dagre.layout(g)

  for (const node of nodes) {
    const pos = g.node(node.id)
    const w = NODE_W[node.data.type] ?? 180
    const h = NODE_H[node.data.type] ?? 56
    node.position = { x: pos.x - w / 2, y: pos.y - h / 2 }
    node.width = w
    node.height = h
  }

  return { nodes, edges }
}
