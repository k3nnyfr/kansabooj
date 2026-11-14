import { useQuery } from '@tanstack/react-query'
import { Fragment } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type ApiRow, type Group } from '../api/client'
import { CheckButton, StatusBadge, TagList } from '../components/common'
import { useI18n } from '../i18n'

type Filter = 'all' | 'unseen' | 'breaking' | 'errors'

const filters: Record<Filter, (a: ApiRow) => boolean> = {
  all: () => true,
  unseen: (a) => a.unacknowledged > 0,
  breaking: (a) => a.unacknowledged_breaking > 0,
  errors: (a) => a.status !== 'ok' && a.status !== 'pending',
}

const NO_GROUP = '_none'

export function ApiList() {
  const { t } = useI18n()
  // Filters live in the URL: shareable links ("all breaking APIs of group X").
  const [params, setParams] = useSearchParams()
  const query = params.get('q') ?? ''
  const filter = (params.get('state') as Filter) || 'all'
  const group = params.get('group') ?? ''
  const tag = params.get('tag') ?? ''
  const groupBy = params.get('by') === 'group'
  const set = (key: string, value: string) =>
    setParams((p) => {
      if (value) p.set(key, value)
      else p.delete(key)
      return p
    }, { replace: true })

  const list = useQuery({ queryKey: ['apis'], queryFn: api.list, refetchInterval: 60_000 })
  const meta = useQuery({ queryKey: ['meta'], queryFn: api.meta, staleTime: Infinity })

  if (list.isLoading) return <p className="muted">{t('loading')}</p>
  if (list.error) return <p className="callout callout-error">{t('error.load', { msg: list.error.message })}</p>

  const groups = meta.data?.groups ?? []
  const groupName = (id?: string) => groups.find((g) => g.id === id)?.name ?? id ?? ''
  const q = query.trim().toLowerCase()
  const scoped = (list.data ?? []).filter(
    (a) =>
      (!group || (group === NO_GROUP ? !a.group : a.group === group)) &&
      (!tag || a.tags.includes(tag)) &&
      (!q || [a.id, a.name, a.owner ?? '', a.title ?? '', groupName(a.group), ...a.tags].some((s) => s.toLowerCase().includes(q))),
  )
  const rows = scoped.filter(filters[filter])
  const count = (f: Filter) => scoped.filter(filters[f]).length
  const sections: Section[] = groupBy ? sectionize(rows, groups) : [{ key: '', title: '', rows }]

  return (
    <>
      <div className="toolbar">
        <h1>{t('list.title')}</h1>
        <div className="toolbar-controls">
          {groups.length > 0 && (
            <select value={group} onChange={(e) => set('group', e.target.value)} aria-label={t('detail.group')} className="select">
              <option value="">{t('list.group.all')}</option>
              {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
              <option value={NO_GROUP}>{t('list.group.none')}</option>
            </select>
          )}
          <input className="search" type="search" placeholder={t('list.search')} value={query}
            onChange={(e) => set('q', e.target.value)} aria-label={t('list.search')} />
        </div>
      </div>

      <div className="chips" role="tablist">
        {(Object.keys(filters) as Filter[]).map((f) => (
          <button key={f} role="tab" aria-selected={filter === f} className={`chip ${filter === f ? 'active' : ''}`}
            onClick={() => set('state', f === 'all' ? '' : f)}>
            {t(`list.filter.${f}`)} <span className="chip-count">{count(f)}</span>
          </button>
        ))}
        {tag && (
          <button className="chip active" onClick={() => set('tag', '')} aria-label={t('list.clearTag', { tag })}>
            #{tag} ✕
          </button>
        )}
        <span className="spacer" />
        {groups.length > 0 && (
          <label className="small toggle">
            <input type="checkbox" checked={groupBy} onChange={(e) => set('by', e.target.checked ? 'group' : '')} /> {t('list.groupBy')}
          </label>
        )}
      </div>

      {(meta.data?.tags.length ?? 0) > 0 && !tag && (
        <div className="tagbar small">
          <span className="muted">{t('list.tags')} :</span>
          {meta.data!.tags.map((x) => <button key={x} className="tag" onClick={() => set('tag', x)}>#{x}</button>)}
        </div>
      )}

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
              {sections.map((s) => (
                <Fragment key={s.key}>
                  {groupBy && (
                    <tr className="section-row">
                      <th colSpan={6}>
                        {s.title || t('list.group.none')} <span className="muted small">· {s.rows.length}</span>
                        {s.description && <div className="small muted section-desc">{s.description}</div>}
                      </th>
                    </tr>
                  )}
                  {s.rows.map((a) => (
                    <Row key={a.id} a={a} groupName={groupBy ? '' : groupName(a.group)} onTag={(x) => set('tag', x)} />
                  ))}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}

function Row({ a, groupName, onTag }: { a: ApiRow; groupName: string; onTag: (t: string) => void }) {
  const { t, relative, date } = useI18n()
  return (
    <tr className={a.unacknowledged_breaking > 0 ? 'row-alert' : ''}>
      <td>
        <Link to={`/apis/${a.id}`} className="api-name">{a.name}</Link>
        <div className="small muted">
          {[groupName, a.owner, a.id].filter(Boolean).join(' · ')}
        </div>
        <TagList tags={a.tags} onClick={onTag} />
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
  )
}

type Section = { key: string; title: string; description?: string; rows: ApiRow[] }

function sectionize(rows: ApiRow[], groups: Group[]): Section[] {
  const sections: Section[] = groups
    .map((g) => ({ key: g.id, title: g.name, description: g.description, rows: rows.filter((a) => a.group === g.id) }))
    .filter((s) => s.rows.length > 0)
  const orphans = rows.filter((a) => !a.group || !groups.some((g) => g.id === a.group))
  if (orphans.length) sections.push({ key: NO_GROUP, title: '', rows: orphans })
  return sections
}
