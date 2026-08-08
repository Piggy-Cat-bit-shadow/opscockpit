import { useCallback, useEffect, useMemo, useState } from 'react'
import { ArrowLeft } from 'lucide-react'
import type { State } from '@/types'
import { validateState, SchemaError } from '@/types'
import { createPollController, restartService } from '@/api'
import { HostSummary } from '@/components/HostSummary'
import { TopologyCanvas } from '@/components/TopologyView'
import { IngressBoard } from '@/components/IngressBoard'
import { ServiceRail } from '@/components/ServiceRail'
import { InspectorDrawer } from '@/components/InspectorDrawer'
import { MobileTopology } from '@/components/MobileTopology'
import { buildIngressBoard, buildPortFocus, buildServiceFocus, type FocusGraph } from '@/topology'
import { COLORS } from '@/theme'

type Mode =
  | { kind: 'overview' }
  | { kind: 'port'; start: number; end: number }
  | { kind: 'service'; serviceId: string }

export default function App() {
  const [state, setState] = useState<State | null>(null)
  const [error, setError] = useState<{ title: string; detail: string } | null>(null)
  const [mode, setMode] = useState<Mode>({ kind: 'overview' })
  const [inspectedId, setInspectedId] = useState<string | null>(null)
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < 768)

  const onData = useCallback((s: State) => {
    setState(s)
    setError(null)
  }, [])
  const onError = useCallback((e: unknown) => {
    if (e instanceof SchemaError) {
      setError({ title: e.kind === 'schema' ? 'Schema mismatch' : 'State unavailable', detail: e.message })
    } else {
      setError({ title: 'State unavailable', detail: e instanceof Error ? e.message : 'API unreachable' })
    }
  }, [])

  useEffect(() => {
    const controller = createPollController((payload) => {
      try {
        onData(validateState(payload))
      } catch (e) {
        onError(e)
      }
    }, onError)
    const stop = controller.subscribe()
    return stop
  }, [onData, onError])

  useEffect(() => {
    const onResize = () => setIsMobile(window.innerWidth < 768)
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  const handleRestart = useCallback(async (serviceId: string) => {
    await restartService(serviceId)
  }, [])

  const cards = useMemo(() => (state ? buildIngressBoard(state.topology) : []), [state])

  const focusGraph: FocusGraph | null = useMemo(() => {
    if (!state) return null
    if (mode.kind === 'port') return buildPortFocus(state.topology, mode.start, mode.end)
    if (mode.kind === 'service') return buildServiceFocus(state.topology, mode.serviceId)
    return null
  }, [state, mode])

  const focusTitle = useMemo(() => {
    if (!state) return ''
    if (mode.kind === 'port') {
      return mode.start === mode.end ? `${mode.start}` : `${mode.start}–${mode.end}`
    }
    if (mode.kind === 'service') {
      return state.services.find((s) => s.id === mode.serviceId)?.name ?? mode.serviceId
    }
    return ''
  }, [state, mode])

  const inspectedService = useMemo(() => {
    if (!state || !inspectedId) return null
    return state.services.find((s) => s.id === inspectedId) ?? null
  }, [state, inspectedId])

  const openPort = useCallback((start: number, end: number) => {
    setMode({ kind: 'port', start, end })
    setInspectedId(null)
  }, [])

  const openService = useCallback((serviceId: string) => {
    setMode({ kind: 'service', serviceId })
    setInspectedId(null)
  }, [])

  const backToOverview = useCallback(() => {
    setMode({ kind: 'overview' })
    setInspectedId(null)
  }, [])

  const selectService = useCallback((serviceId: string) => {
    setInspectedId(serviceId)
  }, [])

  const onKeyDown = useCallback(
    (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        if (inspectedId) setInspectedId(null)
        else setMode({ kind: 'overview' })
      }
    },
    [inspectedId],
  )
  useEffect(() => {
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onKeyDown])

  const inFocus = mode.kind !== 'overview'

  return (
    <div className="oc-app">
      {!state && !error && <div className="oc-loading">Loading…</div>}
      {!state && error && (
        <div className="oc-loading oc-error">
          <strong>{error.title}</strong>
          <span>{error.detail}</span>
          <div className="oc-dim">Run <code>opscockpit collect</code> then <code>opscockpit serve</code>.</div>
        </div>
      )}

      {state && (
        <div className="oc-state-banner">
          {state.health.stale && <div className="oc-stale-banner">STALE — state is older than expected, values may be outdated.</div>}
        </div>
      )}

      {state && isMobile && (
        <div className="oc-mobile">
          <div className="oc-mobile-head">
            <span className="oc-brand">OpsCockpit</span>
            <span
              className="oc-statusbadge"
              style={{
                color: COLORS.green,
                borderColor: `${COLORS.green}55`,
                background: `${COLORS.green}14`,
              }}
            >
              {state.health.status}
            </span>
          </div>
          <HostSummary state={state} />
          <MobileTopology
            state={state}
            selectedService={inspectedService}
            onSelectService={(id) => setInspectedId(id)}
            onCloseDetail={() => setInspectedId(null)}
            onRestart={handleRestart}
          />
        </div>
      )}

      {state && !isMobile && (
        <div className="oc-desktop">
          <header className="oc-header">
            <span className="oc-brand">OpsCockpit</span>
            <span className="oc-dim oc-header-sub">{state.host.hostname}</span>
            <span className="oc-header-spacer" />
            <span className="oc-dim">state {state.health.age_seconds}s old</span>
          </header>

          <div className="oc-main">
            <div className="oc-col-main">
              <div className="oc-toolbar">
                {inFocus && (
                  <button className="oc-toolbar-btn" onClick={backToOverview}>
                    <ArrowLeft size={13} /> All ingress
                  </button>
                )}
                <span className="oc-toolbar-title">
                  {mode.kind === 'overview' ? 'Ingress' : mode.kind === 'port' ? `Port ${focusTitle}` : `Service · ${focusTitle}`}
                </span>
                <span className="oc-toolbar-spacer" />
                {mode.kind === 'service' && (
                  <button className="oc-toolbar-btn" onClick={backToOverview}>
                    Reset
                  </button>
                )}
              </div>

              <div className="oc-content">
                {mode.kind === 'overview' ? (
                  <div className="oc-overview-scroll">
                    <IngressBoard cards={cards} onOpenPort={openPort} />
                  </div>
                ) : (
                  focusGraph && <TopologyCanvas graph={focusGraph} selectedServiceId={inspectedId ?? undefined} onSelectService={selectService} />
                )}
              </div>
            </div>

            <div className="oc-col-right">
              <ServiceRail services={state.services} selectedId={inspectedId ?? undefined} onSelect={openService} />
            </div>
          </div>

          <InspectorDrawer
            service={inspectedService}
            topology={state.topology}
            onClose={() => setInspectedId(null)}
            onRestart={handleRestart}
          />
        </div>
      )}
    </div>
  )
}
