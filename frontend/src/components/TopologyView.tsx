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
import type { State } from '@/types'
import { applyDagreLayout } from '@/layout'
import { nodeTypes } from '@/components/nodes'
import { COLORS } from '@/theme'

interface Props {
  state: State
  onSelectService: (serviceId: string) => void
}

export function TopologyView({ state, onSelectService }: Props) {
  const layout = useMemo(() => applyDagreLayout(state.topology.nodes, state.topology.edges), [state])

  const [nodes, setNodes, onNodesChange] = useNodesState(layout.nodes)
  const [edges, setEdges, onEdgesChange] = useEdgesState(layout.edges)

  useEffect(() => {
    setNodes(layout.nodes)
    setEdges(layout.edges)
  }, [layout, setNodes, setEdges])

  const onNodeClick: NodeMouseHandler = useCallback(
    (_, node) => {
      const data = node.data as { type?: string; service_id?: string }
      if (data.type === 'service' && data.service_id) {
        onSelectService(data.service_id)
      }
    },
    [onSelectService],
  )

  return (
    <div className="oc-topology">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        nodeTypes={nodeTypes}
        onNodeClick={onNodeClick}
        fitView
        fitViewOptions={{ padding: 0.25, maxZoom: 1.2 }}
        minZoom={0.2}
        maxZoom={2}
        proOptions={{ hideAttribution: false }}
        nodesConnectable={false}
        nodesDraggable={false}
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
