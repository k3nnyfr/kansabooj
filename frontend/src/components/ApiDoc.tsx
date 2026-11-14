import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useEffect, useRef } from 'react'
import { api, type Snapshot } from '../api/client'
import { useI18n } from '../i18n'

const SwaggerView = lazy(() => import('./SwaggerView'))

const LARGE = 4 << 20 // above this, listing every operation visibly freezes the tab: only tags are listed

// Documentation section of an API page: Swagger UI rendering of one snapshot (the current one by default).
// shown = snapshot id from the URL (?doc=), so a version's documentation can be linked to.
export function ApiDoc({ apiID, latest, shown, onShow }: {
  apiID: string
  latest: number
  shown?: number
  onShow: (snapshot?: number) => void
}) {
  const { t, relative, date, lang } = useI18n()
  const ref = useRef<HTMLElement>(null)
  const spec = useQuery({
    queryKey: ['spec', shown],
    queryFn: () => fetchSpec(shown!),
    enabled: shown !== undefined,
    staleTime: Infinity, // a snapshot never changes
  })
  const snapshots = useQuery({ queryKey: ['snapshots', apiID], queryFn: () => api.snapshots(apiID), enabled: shown !== undefined })
  const snap: Snapshot | undefined = snapshots.data?.find((s) => s.id === shown)

  useEffect(() => {
    if (shown !== undefined && shown !== latest) ref.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }, [shown, latest])

  return (
    <section ref={ref} className="apidoc" id="doc">
      <h2>{t('doc.title')}</h2>
      {shown === undefined ? (
        <div className="apidoc-closed">
          <span className="small muted">{t('doc.intro')}</span>
          <button className="btn btn-secondary" onClick={() => onShow(latest)}>{t('doc.show')}</button>
        </div>
      ) : (
        <>
          <div className="apidoc-bar small">
            {snap && <span className="muted" title={date(snap.fetched_at)}>{t('doc.version', { v: snap.version || 'n/a', when: relative(snap.fetched_at) })}</span>}
            {shown !== latest && <button className="link" onClick={() => onShow(latest)}>{t('doc.latest')}</button>}
            <a href={api.specUrl(shown, 'normalized')} target="_blank" rel="noreferrer">{t('detail.spec')}</a>
            <span className="spacer" />
            <button className="btn btn-small btn-ghost" onClick={() => onShow(undefined)}>{t('doc.hide')}</button>
          </div>
          {spec.isLoading && <p className="muted small">{t('loading')}</p>}
          {spec.error && <p className="callout callout-error">{t('error.load', { msg: spec.error.message })}</p>}
          {spec.data && (
            <>
              {spec.data.length > LARGE && <div className="callout callout-warn small">{t('doc.large', { size: formatSize(spec.data.length, lang) })}</div>}
              <div className="apidoc-card">
                <Suspense fallback={<p className="muted small">{t('loading')}</p>}>
                  <SwaggerView key={shown} spec={spec.data} large={spec.data.length > LARGE} />
                </Suspense>
              </div>
            </>
          )}
        </>
      )}
    </section>
  )
}

async function fetchSpec(snapshot: number): Promise<string> {
  const res = await fetch(api.specUrl(snapshot, 'normalized'))
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.text()
}

function formatSize(n: number, lang: string): string {
  const mb = (n / (1 << 20)).toLocaleString(lang, { maximumFractionDigits: 1 })
  return `${mb} ${lang === 'fr' ? 'Mo' : 'MB'}`
}
