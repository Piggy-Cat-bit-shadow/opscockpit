import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import App from '@/App'
import type { State } from '@/types'

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
      { id: 'nginx', name: 'Nginx', status: 'healthy', unit: 'nginx.service', unit_state: 'running', version: 'nginx/1.27.1', memory: { rss_bytes: 18 * 1024 * 1024, source: 'proc_rss' }, config_path: '/etc/nginx/nginx.conf', config_exists: true, restart_enabled: true, listeners: [{ protocol: 'tcp', port: 443, address: '0.0.0.0', internal: false, exposure: 'direct_public' }] },
      { id: 'hysteria2', name: 'Hysteria2', status: 'healthy', unit: 'hysteria-server.service', unit_state: 'running', version: 'Hysteria 2.5.0', memory: { rss_bytes: 32 * 1024 * 1024, source: 'cgroup_memory_current' }, config_path: '/etc/hysteria/config.yaml', config_exists: true, restart_enabled: true, listeners: [{ protocol: 'udp', port: 443, address: '::', internal: false, exposure: 'direct_public' }] },
      { id: 'xray', name: 'Xray', status: 'failed', unit: 'xray.service', unit_state: 'failed', restart_enabled: false, listeners: [{ protocol: 'tcp', port: 18444, address: '127.0.0.1', internal: true }] },
    ],
    health: { status: 'healthy', stale: false, age_seconds: 3, services_healthy: 2, services_warning: 0, services_failed: 1, services_unknown: 0 },
    topology: {
      nodes: [
        { id: 'internet', type: 'internet', label: 'Internet' },
        { id: 'port-443', type: 'port', label: '443', port: 443, port_start: 443, port_end: 443 },
        { id: 'port-443-tcp', type: 'protocol', label: 'TCP', protocol: 'tcp', port: 443, port_start: 443, port_end: 443 },
        { id: 'nginx@tcp:443', type: 'service', label: 'Nginx', service_id: 'nginx', protocol: 'tcp', port: 443, port_start: 443, port_end: 443, status: 'healthy' },
        { id: 'port-443-udp', type: 'protocol', label: 'UDP', protocol: 'udp', port: 443, port_start: 443, port_end: 443 },
        { id: 'hysteria2@udp:443', type: 'service', label: 'Hysteria2', service_id: 'hysteria2', protocol: 'udp', port: 443, port_start: 443, port_end: 443, status: 'healthy' },
        { id: 'xray@dep:tcp:18444', type: 'service', label: 'Xray', service_id: 'xray', protocol: 'tcp', port: 18444, status: 'failed' },
      ],
      edges: [
        { id: 'e1', source: 'internet', target: 'port-443' },
        { id: 'e2', source: 'port-443', target: 'port-443-tcp' },
        { id: 'e3', source: 'port-443-tcp', target: 'nginx@tcp:443' },
        { id: 'e4', source: 'port-443', target: 'port-443-udp' },
        { id: 'e5', source: 'port-443-udp', target: 'hysteria2@udp:443' },
        { id: 'e6', source: 'nginx@tcp:443', target: 'xray@dep:tcp:18444' },
      ],
    },
  }
}

beforeEach(() => {
  const st = mockState()
  vi.stubGlobal('fetch', vi.fn((url: string) => {
    if (url.includes('/api/state')) {
      return Promise.resolve(new Response(JSON.stringify(st), { status: 200, headers: { ETag: '"abc"' } }))
    }
    return Promise.resolve(new Response('{}', { status: 200 }))
  }))
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('App overview (Ingress Board)', () => {
  it('renders the ingress board with port cards', async () => {
    render(<App />)
    expect(await screen.findAllByText('443').then((els) => els.length > 0)).toBe(true)
    // Ingress title
    expect(screen.getByText('Ingress')).toBeTruthy()
    // Service rail shows the services
    expect(screen.getAllByText('Nginx').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Hysteria2').length).toBeGreaterThan(0)
  })

  it('opens Port Focus when a card is clicked', async () => {
    render(<App />)
    const portEls = await screen.findAllByText('443')
    // Click the card header (the big port number).
    fireEvent.click(portEls[0])
    expect(await screen.findByText(/Port 443/)).toBeTruthy()
    // The focus canvas shows the back link.
    expect(screen.getByText('All ingress')).toBeTruthy()
  })
})

describe('App mobile', () => {
  it('renders the vertical flow and bottom sheet', async () => {
    window.innerWidth = 390
    window.dispatchEvent(new Event('resize'))
    render(<App />)
    expect(await screen.findAllByText('443').then((els) => els.length > 0)).toBe(true)
    // Expand the 443 card.
    const head = screen.getAllByText('443')[0]
    fireEvent.click(head)
    // Click a service leaf → bottom sheet shows restart.
    const nginxLeaf = await screen.findAllByText('Nginx').then((els) => els.find((e) => e.className.includes('oc-mproto-svc')))
    if (nginxLeaf) fireEvent.click(nginxLeaf)
    expect(await screen.findByText('Restart service')).toBeTruthy()
  })
})
