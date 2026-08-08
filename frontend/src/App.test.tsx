import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import App from '@/App'
import type { State } from '@/types'

// Build a full mock state matching the spec testdata.
function mockState(): State {
  return {
    schema_version: 1,
    generated_at: new Date().toISOString(),
    collector_version: 'test',
    collect_duration_ms: 42,
    host: {
      hostname: 'mock-vps',
      uptime_seconds: 259200,
      cpu: { cores: 4, percent: 12.5 },
      memory: { total_bytes: 8 << 30, used_bytes: 2 << 30, percent: 25 },
      swap: { total_bytes: 0, used_bytes: 0, percent: 0 },
      disk: { mount_point: '/', total_bytes: 100 << 30, used_bytes: 40 << 30, percent: 40 },
      load: { load1: 0.4, load5: 0.3, load15: 0.2 },
    },
    services: [
      { id: 'nginx', name: 'Nginx', status: 'healthy', unit: 'nginx.service', unit_state: 'running', version: 'nginx/1.27.1', memory: { rss_bytes: 18 * 1024 * 1024, source: 'proc_rss' }, config_path: '/etc/nginx/nginx.conf', config_exists: true, restart_enabled: true, listeners: [{ protocol: 'tcp', port: 443, address: '0.0.0.0', internal: false }] },
      { id: 'hysteria2', name: 'Hysteria2', status: 'healthy', unit: 'hysteria-server.service', unit_state: 'running', version: 'Hysteria 2.5.0', memory: { rss_bytes: 32 * 1024 * 1024, source: 'cgroup_memory_current' }, config_path: '/etc/hysteria/config.yaml', config_exists: true, restart_enabled: true, listeners: [{ protocol: 'udp', port: 443, address: '::', internal: false }] },
      { id: 'xray', name: 'Xray', status: 'failed', unit: 'xray.service', unit_state: 'failed', restart_enabled: false, listeners: [{ protocol: 'tcp', port: 18444, address: '127.0.0.1', internal: true }] },
    ],
    health: { status: 'healthy', stale: false, age_seconds: 3, services_healthy: 2, services_warning: 0, services_failed: 1, services_unknown: 0 },
    topology: {
      nodes: [
        { id: 'internet', type: 'internet', label: 'Internet' },
        { id: 'port-443', type: 'port', label: '443', port: 443 },
        { id: 'port-443-tcp', type: 'protocol', label: 'TCP', protocol: 'tcp', port: 443 },
        { id: 'nginx@tcp:443', type: 'service', label: 'Nginx', service_id: 'nginx', protocol: 'tcp', port: 443, status: 'healthy' },
        { id: 'port-443-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 443 },
        { id: 'hysteria2@udp:443', type: 'service', label: 'Hysteria2', service_id: 'hysteria2', protocol: 'udp', port: 443, status: 'healthy' },
        { id: 'xray@dep:tcp:18444', type: 'service', label: 'Xray', service_id: 'xray', protocol: 'tcp', port: 18444, status: 'failed' },
      ],
      edges: [
        { id: 'e1', source: 'internet', target: 'port-443' },
        { id: 'e2', source: 'port-443', target: 'port-443-tcp' },
        { id: 'e3', source: 'port-443-tcp', target: 'nginx@tcp:443' },
        { id: 'e4', source: 'port-443', target: 'port-443-udp' },
        { id: 'e5', source: 'port-443-udp', target: 'hysteria2@udp:443' },
      ],
    },
  }
}

// Stub fetch so the polling hook returns the mock state once.
beforeEach(() => {
  const st = mockState()
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (url.includes('/api/state')) {
      return Promise.resolve(
        new Response(JSON.stringify(st), { status: 200, headers: { ETag: '"abc"' } }),
      )
    }
    return Promise.resolve(new Response('{}', { status: 200 }))
  }))
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('App desktop rendering', () => {
  it('shows host summary and service list', async () => {
    render(<App />)
    // Host summary (hostname appears in header + host card)
    expect((await screen.findAllByText('mock-vps')).length).toBeGreaterThan(0)
    expect(screen.getByText('OpsCockpit')).toBeTruthy()

    // Service list shows all three services, failed first.
    expect(screen.getAllByText('Nginx').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Hysteria2').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Xray').length).toBeGreaterThan(0)

    // Service rows in order: Xray (failed) first.
    const rows = screen.getAllByRole('button').filter((b) => b.className.includes('oc-svcrow'))
    expect(rows[0].textContent).toContain('Xray')
  })
})

describe('App mobile rendering', () => {
  it('renders the collapsible tree and bottom sheet', async () => {
    window.innerWidth = 390
    window.dispatchEvent(new Event('resize'))
    render(<App />)

    expect(await screen.findByText('Internet')).toBeTruthy()
    // Port 443 visible in the tree; ports start auto-expanded (do NOT click,
    // or it would toggle the port closed).
    const portHeads = screen.getAllByText('443')
    expect(portHeads.length).toBeGreaterThan(0)

    // Expand the TCP protocol to reveal the Nginx leaf.
    const tcpProto = screen.getAllByText('TCP').find((el) => el.className.includes('oc-tree-proto-label'))
    fireEvent.click(tcpProto!)

    const nginxLeaf = await screen.findByText('Nginx')
    fireEvent.click(nginxLeaf)

    // Bottom sheet shows service details.
    expect(await screen.findByText('Restart service')).toBeTruthy()
  })
})
