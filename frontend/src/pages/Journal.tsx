import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type JournalEntry, type JournalKind } from '../api/client'
import { useI18n } from '../i18n'

const KINDS: JournalKind[] = ['check_manual', 'check_scheduled', 'ack', 'notification']

// Journal of the actions taken: manual and scheduled checks, acknowledgments, notifications.
// Filters live in the URL (?kind=…&api=…) so that a view can be shared.
export function Journal() {
  const { t } = useI18n()
  const [params, setParams] = useSearchParams()
  const kind = KINDS.find((k) => k === params.get('kind'))
  const apiID = params.get('api') ?? ''
  const set = (key: string, value: string) =>
    setParams((p) => {
      if (value) p.set(key, value)
      else p.delete(key)
      return p
    }, { replace: true })

  const apis = useQuery({ queryKey: ['apis'], queryFn: api.list })
  const journal = useInfiniteQuery({
    queryKey: ['journal', kind, apiID],
    queryFn: ({ pageParam }) => api.journal({ before: pageParam, kinds: kind ? [kind] : [], api: apiID || undefined }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next,
    refetchInterval: 30_000,
  })
  const entries = journal.data?.pages.flatMap((p) => p.entries) ?? []

  return (
    <>
      <div className="toolbar">
        <h1>{t('journal.title')}</h1>
        <div className="toolbar-controls">
          <select className="select" value={apiID} onChange={(e) => set('api', e.target.value)} aria-label={t('journal.col.api')}>
            <option value="">{t('journal.allApis')}</option>
            {(apis.data ?? []).slice().sort((a, b) => a.name.localeCompare(b.name)).map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </div>
      </div>
      <p className="small muted journal-intro">{t('journal.intro')}</p>

      <div className="chips" role="tablist">
        {[undefined, ...KINDS].map((k) => (
          <button key={k ?? 'all'} role="tab" aria-selected={kind === k} className={`chip ${kind === k ? 'active' : ''}`}
            onClick={() => set('kind', k ?? '')}>
            {t(k ? `journal.filter.${k}` : 'journal.filter.all')}
          </button>
        ))}
      </div>

      {journal.error && <p className="callout callout-error">{t('error.load', { msg: journal.error.message })}</p>}
      <div className="table-wrap">
        <table className="table journal-table">
          <colgroup>
            <col className="c-when" />
            <col className="c-kind" />
            <col className="c-api" />
            <col className="c-by" />
            <col />
          </colgroup>
          <thead>
            <tr>
              <th>{t('journal.col.when')}</th>
              <th>{t('journal.col.action')}</th>
              <th>{t('journal.col.api')}</th>
              <th>{t('journal.col.by')}</th>
              <th>{t('journal.col.result')}</th>
            </tr>
          </thead>
          <tbody>
            {journal.isLoading && (
              <tr><td colSpan={5} className="muted">{t('loading')}</td></tr>
            )}
            {!journal.isLoading && entries.length === 0 && (
              <tr><td colSpan={5} className="muted">{t('journal.empty')}</td></tr>
            )}
            {entries.map((e, i) => <Row key={`${e.kind}:${e.at}:${e.api_id}:${e.change_id ?? ''}:${e.notifier ?? ''}:${i}`} e={e} />)}
          </tbody>
        </table>
      </div>
      {journal.hasNextPage && (
        <p>
          <button className="btn btn-secondary" onClick={() => journal.fetchNextPage()} disabled={journal.isFetchingNextPage}>
            {t('action.loadMore')}
          </button>
        </p>
      )}
    </>
  )
}

function kindOf(e: JournalEntry): JournalKind {
  if (e.kind === 'check') return e.trigger === 'manual' ? 'check_manual' : 'check_scheduled'
  return e.kind
}

const kindBadge: Record<JournalKind, string> = {
  check_manual: 'level-info',
  check_scheduled: 'status-pending',
  ack: 'status-ok',
  notification: 'pending',
}

function Row({ e }: { e: JournalEntry }) {
  const { t, date, relative } = useI18n()
  const k = kindOf(e)
  const changeLink = e.change_id ? `/apis/${encodeURIComponent(e.api_id)}?change=${e.change_id}` : undefined
  return (
    <tr className={e.status === 'error' || e.status === 'failed' ? 'row-alert' : ''}>
      <td>
        <div>{relative(e.at)}</div>
        <div className="small muted">{date(e.at)}</div>
      </td>
      <td><span className={`badge ${kindBadge[k]}`}>{t(`journal.kind.${k}`)}</span></td>
      <td className="ellipsis">
        <Link to={`/apis/${encodeURIComponent(e.api_id)}`} className="api-name" title={e.api_id}>{e.api_name}</Link>
      </td>
      <td className="ellipsis">
        {e.kind === 'notification' ? (
          <span className="mono small" title={e.notifier}>{e.notifier}</span>
        ) : e.actor ? (
          <span title={e.actor}>{e.actor}</span>
        ) : e.trigger === 'manual' ? (
          <span className="muted">—</span> // manual check recorded before v0.9: no author
        ) : (
          <span className="muted">{t(e.trigger === 'startup' ? 'journal.by.startup' : 'journal.by.scheduler')}</span>
        )}
      </td>
      <td>
        {e.kind === 'check' && (
          <span className="journal-result">
            <span className={`dot dot-${e.status}`} aria-hidden />
            {t(`journal.check.${e.status}` as 'journal.check.error')}
            {e.duration_ms !== undefined && <span className="muted"> · {e.duration_ms} ms</span>}
            {e.error && <span className="small muted journal-error" title={e.error}> · {e.error}</span>}
          </span>
        )}
        {e.kind === 'ack' && (
          <span className="journal-result">
            {e.version ? t('journal.ack', { v: e.version }) : t('journal.ackNoVersion')}
            {changeLink && <> · <Link to={changeLink}>{t('journal.see')}</Link></>}
          </span>
        )}
        {e.kind === 'notification' && (
          <span className="journal-result">
            {t(`journal.notif.${e.status}` as 'journal.notif.sent')}
            {e.notification_kind && <span className="muted"> · {t(`journal.notifKind.${e.notification_kind}` as 'journal.notifKind.change')}</span>}
            {e.status === 'failed' && e.attempts ? <span className="muted"> · {t('journal.notif.attempts', { n: e.attempts })}</span> : null}
            {e.error && <span className="small muted journal-error" title={e.error}> · {e.error}</span>}
            {changeLink && <> · <Link to={changeLink}>{t('journal.see')}</Link></>}
          </span>
        )}
      </td>
    </tr>
  )
}
