import { describe, it, expect } from 'vitest'
import bigState from './__bigstate'
import { buildIngressBoard, buildPortFocus, buildServiceFocus } from './topology'

// This test uses the real production-scale fixture (19 services, ~36 nodes)
// to prove the board is readable (small graph per port) and focus subgraphs
// are small by construction — the core of the redesign.

describe('production-scale fixture', () => {
  const state = bigState as unknown as {
    topology: { nodes: { id: string; type: string; label: string; port?: number; port_start?: number; port_end?: number }[]; edges: { source: string; target: string }[] }
  }
  const topo = state.topology as never

  it('board has one card per public port, all readable', () => {
    const cards = buildIngressBoard(topo)
    // Every port node yields a card (no card 0 / no dupes).
    expect(cards.length).toBeGreaterThan(5)
    for (const c of cards) {
      expect(c.portStart).toBeGreaterThan(0)
      expect(c.protocols.length).toBeGreaterThan(0)
    }
    // Deterministic ascending order.
    const starts = cards.map((c) => c.portStart)
    expect([...starts].sort((a, b) => a - b)).toEqual(starts)
  })

  it('Port Focus for 443 is a small readable subgraph (not the full tree)', () => {
    const g = buildPortFocus(topo, 443)
    // A single port focus must be a handful of nodes — never the full 36.
    expect(g.nodes.length).toBeLessThan(10)
    expect(g.nodes.some((n) => n.type === 'port')).toBe(true)
    // Only the 443 branch, no other ports.
    expect(g.nodes.some((n) => n.type === 'port' && n.portStart !== 443)).toBe(false)
  })

  it('Port Focus for the NAT range shows its target chain', () => {
    const g = buildPortFocus(topo, 20000, 20099)
    expect(g.nodes.some((n) => n.type === 'port')).toBe(true)
  })

  it('Service Focus for xray finds its upstream entrypoints', () => {
    const g = buildServiceFocus(topo, 'xray')
    const ports = g.nodes.filter((n) => n.type === 'port').map((n) => n.portStart)
    expect(ports.length).toBeGreaterThan(0)
    // Subgraph stays small even at production scale.
    expect(g.nodes.length).toBeLessThan(16)
  })

  it('every service focus stays small (no full-tree explosion)', () => {
    const serviceIds = new Set(state.topology.nodes.filter((n) => n.type === 'service').map((n) => (n as { service_id?: string }).service_id).filter(Boolean))
    for (const id of serviceIds) {
      const g = buildServiceFocus(topo, id!)
      expect(g.nodes.length).toBeLessThan(20)
    }
  })
})
