import { useCallback, useEffect, useMemo } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  useNodesState,
  useEdgesState,
  type NodeMouseHandler,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { FocusGraph, FocusNode } from '@/topology'
import { applyFocusLayout } from '@/layout'
import { nodeTypes } from '@/components/nodes'
import { COLORS } from '@/theme'

interface Props {
  graph: FocusGraph
  selectedServiceId?: string
  onSelectService: (serviceId: string) => void
}

/**
 * TopologyCanvas renders ONE Focus subgraph with React Flow, left→right, at a
 * readable zoom. It never fitViews the whole 48-node tree — the graph shown is
 * small by construction (Port Focus / Service Focus). Selected-path edges are
 * highlighted; unrelated edges dim.
 */
export function TopologyCanvas({ graph, selectedServiceId, onSelectService }: Props) {
  const layout = useMemo(() => applyFocusLayout(graph), [graph])

  const [nodes, setNodes, onNodesChange] = useNodesState(layout.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState(layout.edges)

  useEffect(() => {
    setNodes(layout.nodes)
    setEdges(layout.edges)
  }, [layout, setNodes, setEdges])

  // Dim edges not on the path to/from the selected service.
  const highlighted = useMemo(() => {
    if (!selectedServiceId) return new Set<string>()
    const set = new Set<string>()
    const nodeIds = new Set(
      graph.nodes.filter((n) => n.serviceId === selectedServiceId).map((n) => n.id),
    )
    // Any edge touching a selected node is part of the focused path.
    for (const e of graph.edges) {
      if (nodeIds.has(e.source) || nodeIds.has(e.target)) set.add(e.id)
    }
    return set
  }, [graph, selectedServiceId])

  const styledEdges = useMemo(
    () =>
      edges.map((e) => {
        const active = highlighted.size === 0 || highlighted.has(e.id)
        return {
          ...e,
          style: {
            stroke: active ? COLORS.border : `${COLORS.border}33`,
            strokeWidth: active ? 1.5 : 1,
          },
        }
      }),
    [edges, highlighted],
  )

  const onNodeClick: NodeMouseHandler = useCallback(
    (_, node) => {
      const data = node.data as FocusNode
      if (data.type === 'service' && data.serviceId) {
        onSelectService(data.serviceId)
      }
    },
    [onSelectService],
  )

  return (
    <div className="oc-topology">
      <ReactFlow
        nodes={nodes}
        edges={styledEdges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        onNodeClick={onNodeClick}
        fitView
        fitViewOptions={{ padding: 0.3, minZoom: 0.85, maxZoom: 1 }}
        minZoom={0.5}
        maxZoom={2}
        nodesConnectable={false}
        nodesDraggable
        elementsSelectable
        defaultEdgeOptions={{ style: { stroke: COLORS.border, strokeWidth: 1.5 } }}
        colorMode="dark"
      >
        <Background color="#262b33" gap={24} size={1} />
        <Controls showInteractive={false} />
      </ReactFlow>
    </div>
  )
}
