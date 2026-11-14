import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type ApiRow } from '../api/client'
import { CheckButton, StatusBadge } from '../components/common'
import { useI18n } from '../i18n'

type Filter = 'all' | 'unseen' | 'breaking' | 'errors'

const filters: Record<Filter, (a: ApiRow) => boolean> = {
  all: () => true,
  unseen: (a) => a.unacknowledged > 0,
  breaking: (a) => a.unacknowledged_breaking > 0,
  errors: (a) => a.status !== 'ok' && a.status !== 'pending',
}

export function ApiList() {
  const { t, relative, date } = useI18n()
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const { data, error, isLoading } = useQuery({ queryKey: ['apis'], queryFn: api.list, refetchInterval: 60_000 })

  if (isLoading) return <p className="muted">{t('loading')}</p>
  if (error) return <p className="callout callout-error">{t('error.load', { msg: error.message })}</p>

  const q = query.trim().toLowerCase()
  const rows = (data ?? []).filter(
    (a) => filters[filter](a) && (!q || [a.id, a.name, a.owner ?? '', a.title ?? ''].some((s) => s.toLowerCase().includes(q))),
  )
  const count = (f: Filter) => (data ?? []).filter(filters[f]).length

  return (
    <>
      <div className="toolbar">
        <h1>{t('list.title')}</h1>
        <input
          className="search"
          type="search"
          placeholder={t('list.search')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label={t('list.search')}
        />
      </div>
      <div className="chips" role="tablist">
        {(Object.keys(filters) as Filter[]).map((f) => (
          <button key={f} role="tab" aria-selected={filter === f} className={`chip ${filter === f ? 'active' : ''}`} onClick={() => setFilter(f)}>
            {t(`list.filter.${f}`)} <span className="chip-count">{count(f)}</span>
          </button>
        ))}
      </div>

      {rows.length === 0 ? (
        <p className="muted">{t('list.empty')}</p>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th>{t('col.api')}</th>
                <th>{t('col.status')}</th>
                <th>{t('col.version')}</th>
                <th>{t('col.lastCheck')}</th>
                <th>{t('col.changes')}</th>
                <th aria-hidden />
              </tr>
            </thead>
            <tbody>
              {rows.map((a) => (
                <tr key={a.id} className={a.unacknowledged_breaking > 0 ? 'row-alert' : ''}>
                  <td>
                    <Link to={`/apis/${a.id}`} className="api-name">{a.name}</Link>
                    <div className="small muted">{a.owner ? `${a.owner} · ` : ''}{a.id}</div>
                  </td>
                  <td><StatusBadge status={a.status} /></td>
                  <td className="mono">{a.version || '—'}</td>
                  <td title={date(a.last_checked_at)}>{relative(a.last_checked_at)}</td>
                  <td>
                    {a.unacknowledged > 0 ? (
                      <Link to={`/apis/${a.id}`} className="badge unseen">
                        {t('badge.unseen', { n: a.unacknowledged })}
                        {a.unacknowledged_breaking > 0 && <> · {t('badge.breaking')}</>}
                      </Link>
                    ) : (
                      <span className="muted small" title={date(a.last_change_at)}>{a.last_change_at ? relative(a.last_change_at) : '—'}</span>
                    )}
                  </td>
                  <td className="right"><CheckButton id={a.id} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
