import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { api, type CheckResult, type TimelineEntry } from '../api/client'
import { CheckButton, Counts, ErrorHint, LevelBadge, StatusBadge } from '../components/common'
import { useI18n } from '../i18n'

export function ApiDetail() {
  const { id = '' } = useParams()
  const { t, relative, date } = useI18n()
  const qc = useQueryClient()
  const [params] = useSearchParams()
  const focus = Number(params.get('change')) || undefined
  const [lastCheck, setLastCheck] = useState<CheckResult>()

  const detail = useQuery({ queryKey: ['api', id], queryFn: () => api.get(id), refetchInterval: 60_000 })
  const timeline = useInfiniteQuery({
    queryKey: ['timeline', id],
    queryFn: ({ pageParam }) => api.timeline(id, pageParam),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (last) => (last.length === 30 ? last[last.length - 1].id : undefined),
  })
  const ackAll = useMutation({ mutationFn: () => api.ackAll(id), onSuccess: () => qc.invalidateQueries() })

  if (detail.isLoading) return <p className="muted">{t('loading')}</p>
  if (detail.error) return <p className="callout callout-error">{detail.error.message === 'API inconnue' ? t('notFound') : t('error.load', { msg: detail.error.message })}</p>
  const { api: a, config, checks } = detail.data!
  const entries = timeline.data?.pages.flat() ?? []
  const scope = config.gateway?.exposed_operations ?? []

  return (
    <>
      <Link to="/" className="back">{t('back')}</Link>
      <header className="detail-head">
        <div>
          <h1>{a.name} <StatusBadge status={a.status} /></h1>
          <div className="muted">
            {a.owner && <>{a.owner} · </>}
            <span className="mono">{a.version || '—'}</span>
            {a.operation_count > 0 && <> · {t('detail.operations', { n: a.operation_count })}</>}
            {config.interval && <> · {t('detail.interval', { d: config.interval })}</>}
            {' · '}
            <span title={date(a.last_checked_at)}>{t('col.lastCheck')} {relative(a.last_checked_at)}</span>
          </div>
          <div className="small">
            {t('detail.source')} : <a href={a.source_url} target="_blank" rel="noreferrer" className="mono">{a.source_url}</a>
          </div>
        </div>
        <div className="actions">
          {a.unacknowledged > 0 && (
            <button className="btn btn-secondary" onClick={() => ackAll.mutate()} disabled={ackAll.isPending}>{t('action.ackAll')}</button>
          )}
          <CheckButton id={a.id} onResult={setLastCheck} />
        </div>
      </header>

      {lastCheck && <CheckFeedback r={lastCheck} />}
      {a.status !== 'ok' && a.status !== 'pending' && (
        <ErrorHint kind={a.last_error_kind} detail={a.last_error} consecutive={a.consecutive_errors} />
      )}

      <div className="grid">
        <section>
          <h2>{t('detail.timeline')}</h2>
          {entries.length === 0 && !timeline.isLoading && <p className="muted">{t('detail.noChanges')}</p>}
          <ol className="timeline">
            {entries.map((e) => (
              <TimelineCard key={e.id} entry={e} focused={e.id === focus} />
            ))}
          </ol>
          {timeline.hasNextPage && (
            <button className="btn btn-secondary" onClick={() => timeline.fetchNextPage()} disabled={timeline.isFetchingNextPage}>
              {t('action.loadMore')}
            </button>
          )}
        </section>

        <aside>
          <h2>{t('detail.scope')}</h2>
          {scope.length ? (
            <ul className="scope mono small">{scope.map((op) => <li key={op}>{op}</li>)}</ul>
          ) : (
            <p className="small muted">{t('detail.scope.all')}</p>
          )}
          <h2>{t('detail.checks')}</h2>
          <ul className="checks small">
            {checks.map((c, i) => (
              <li key={i} title={c.error}>
                <span className={`dot dot-${c.status}`} aria-hidden />
                <span title={date(c.started_at)}>{relative(c.started_at)}</span>
                <span className="muted"> · {t(`trigger.${c.trigger}` as 'trigger.manual')} · {c.duration_ms} ms</span>
                {c.http_status ? <span className="muted"> · HTTP {c.http_status}</span> : null}
              </li>
            ))}
          </ul>
        </aside>
      </div>
    </>
  )
}

function CheckFeedback({ r }: { r: CheckResult }) {
  const { t } = useI18n()
  if (r.status === 'error') return <ErrorHint kind={r.error_kind} detail={r.error} />
  const msg = r.status === 'changed' ? t('check.changed') : r.not_modified ? t('check.notModified') : t('check.unchanged')
  return <div className={`callout ${r.status === 'changed' ? 'callout-warn' : 'callout-ok'}`}>{msg} · {r.duration_ms} ms</div>
}

function TimelineCard({ entry: e, focused }: { entry: TimelineEntry; focused: boolean }) {
  const { t, date, relative } = useI18n()
  const qc = useQueryClient()
  const [open, setOpen] = useState(focused || (!e.acknowledged_at && !e.first_capture))
  const [showOut, setShowOut] = useState(false)
  const ref = useRef<HTMLLIElement>(null)
  useEffect(() => {
    if (focused) ref.current?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }, [focused])

  const detail = useQuery({ queryKey: ['change', e.id], queryFn: () => api.change(e.id), enabled: open && !e.first_capture })
  const ack = useMutation({ mutationFn: () => api.ack(e.id), onSuccess: () => qc.invalidateQueries() })

  const unseen = !e.acknowledged_at && !e.first_capture
  const items = detail.data?.items ?? []
  const inScope = items.filter((i) => i.in_scope)
  const outScope = items.length - inScope.length
  const shown = showOut ? items : inScope
  const version =
    e.first_capture ? t('change.first')
    : e.version_from !== e.version_to ? t('change.version', { from: e.version_from || 'n/a', to: e.version_to || 'n/a' })
    : t('change.sameVersion', { v: e.version_to || 'n/a' })

  return (
    <li ref={ref} className={`tl-item ${unseen ? 'unseen' : ''} ${e.in_scope_errors > 0 ? 'breaking' : ''} ${focused ? 'focused' : ''}`}>
      <div className="tl-marker" aria-hidden />
      <div className="tl-card">
        <button className="tl-head" onClick={() => setOpen(!open)} aria-expanded={open} disabled={e.first_capture}>
          <span className="tl-date" title={date(e.detected_at)}>{date(e.detected_at)}</span>
          <span className="tl-version">{version}</span>
          {!e.first_capture && <Counts errors={e.errors} warns={e.warns} infos={e.infos} />}
          {unseen && <span className="badge unseen">{t('change.new')}</span>}
        </button>
        {open && !e.first_capture && (
          <div className="tl-body">
            {detail.isLoading && <p className="muted small">{t('loading')}</p>}
            {detail.data && shown.length === 0 && <p className="muted small">{t('change.noInScope')}</p>}
            <ul className="items">
              {shown.map((it, i) => (
                <li key={i} className={it.in_scope ? '' : 'out'}>
                  <LevelBadge level={it.level} />
                  {(it.method || it.path) && <span className="mono op">{it.method} {it.path}</span>}
                  <span className="text">{it.text}</span>
                  {!it.in_scope && <span className="small muted"> ({t('level.outOfScope')})</span>}
                </li>
              ))}
            </ul>
            <div className="tl-foot small">
              {outScope > 0 && (
                <button className="link" onClick={() => setShowOut(!showOut)}>
                  {showOut ? t('action.hideOut') : t('action.showOut', { n: outScope })}
                </button>
              )}
              <a href={api.specUrl(e.to_snapshot_id, 'normalized')} target="_blank" rel="noreferrer">{t('detail.spec')}</a>
              <a href={api.specUrl(e.to_snapshot_id, 'raw')} target="_blank" rel="noreferrer">{t('detail.specRaw')}</a>
              <span className="spacer" />
              {e.acknowledged_at ? (
                <span className="muted" title={date(e.acknowledged_at)}>{t('change.seenBy', { who: e.acknowledged_by ?? '?', when: relative(e.acknowledged_at) })}</span>
              ) : (
                <button className="btn btn-small" onClick={() => ack.mutate()} disabled={ack.isPending}>{t('action.ack')}</button>
              )}
            </div>
          </div>
        )}
      </div>
    </li>
  )
}
