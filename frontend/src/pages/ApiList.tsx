import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Fragment, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type ApiRow, type ApiStatus, type Group } from '../api/client'
import { CheckButton, StatusBadge, checkKey } from '../components/common'
import { FacetFilter, Segmented, TriCheckbox, type FacetOption } from '../components/filters'
import { useI18n } from '../i18n'

type Filter = 'all' | 'pending' | 'breaking' | 'errors'

const filters: Record<Filter, (a: ApiRow) => boolean> = {
  all: () => true,
  pending: (a) => a.unacknowledged > 0,
  breaking: (a) => a.unacknowledged_breaking > 0,
  errors: (a) => a.status !== 'ok' && a.status !== 'pending',
}

type SortKey = 'name' | 'status' | 'version' | 'checked' | 'changes'
type Dir = 'asc' | 'desc'

const statusRank: Record<ApiStatus, number> = { ok: 0, pending: 1, stale: 2, error: 3, auth_error: 4 }
const time = (iso?: string) => (iso ? new Date(iso).getTime() : 0)
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

// Ascending comparators. Columns whose interesting values are the "big" ones (state,
// last check, pending changes) are first sorted descending when clicked.
const sorters: Record<SortKey, { cmp: (a: ApiRow, b: ApiRow) => number; first: Dir }> = {
  name: { cmp: (a, b) => collator.compare(a.name, b.name), first: 'asc' },
  status: { cmp: (a, b) => statusRank[a.status] - statusRank[b.status], first: 'desc' },
  version: { cmp: (a, b) => collator.compare(a.version ?? '', b.version ?? ''), first: 'desc' },
  checked: { cmp: (a, b) => time(a.last_checked_at) - time(b.last_checked_at), first: 'desc' },
  changes: {
    cmp: (a, b) =>
      a.unacknowledged_breaking - b.unacknowledged_breaking ||
      a.unacknowledged - b.unacknowledged ||
      time(a.last_change_at) - time(b.last_change_at),
    first: 'desc',
  },
}

const NO_GROUP = '_none'
const BULK_CONCURRENCY = 4

type BulkState =
  | { kind: 'running'; label: 'bulk.progress' | 'bulk.ackProgress'; done: number; total: number }
  | { kind: 'done'; message: string; failures: string[] }

export function ApiList() {
  const { t } = useI18n()
  const qc = useQueryClient()
  // Filters, view and sort live in the URL: shareable links ("breaking APIs of groups X and Y").
  const [params, setParams] = useSearchParams()
  const query = params.get('q') ?? ''
  const state = params.get('state')
  const filter: Filter = state === 'unseen' ? 'pending' : state && state in filters ? (state as Filter) : 'all'
  const groupSel = params.getAll('group')
  const tagSel = params.getAll('tag')
  const byGroup = params.get('by') === 'group'
  const sortKey: SortKey = (params.get('sort') as SortKey) in sorters ? (params.get('sort') as SortKey) : 'name'
  const dir: Dir = params.get('dir') === 'desc' ? 'desc' : params.get('dir') === 'asc' ? 'asc' : sorters[sortKey].first
  const update = (fn: (p: URLSearchParams) => void) => setParams((p) => (fn(p), p), { replace: true })
  const set = (key: string, value: string) => update((p) => (value ? p.set(key, value) : p.delete(key)))
  const setAll = (key: string, values: string[]) =>
    update((p) => {
      p.delete(key)
      values.forEach((v) => p.append(key, v))
    })
  const addTo = (key: 'group' | 'tag', v: string) => !params.getAll(key).includes(v) && update((p) => p.append(key, v))
  const sortBy = (k: SortKey) =>
    update((p) => {
      p.set('sort', k)
      p.set('dir', k === sortKey ? (dir === 'asc' ? 'desc' : 'asc') : sorters[k].first)
    })

  const [selection, setSelection] = useState<Set<string>>(new Set())
  const [bulk, setBulk] = useState<BulkState | null>(null)
  const checkOne = useMutation({ mutationKey: checkKey, mutationFn: (id: string) => api.check(id) })
  const ackOne = useMutation({ mutationFn: (id: string) => api.ackAll(id) })

  const list = useQuery({ queryKey: ['apis'], queryFn: api.list, refetchInterval: 60_000 })
  const meta = useQuery({ queryKey: ['meta'], queryFn: api.meta, staleTime: Infinity })

  if (list.isLoading) return <p className="muted">{t('loading')}</p>
  if (list.error) return <p className="callout callout-error">{t('error.load', { msg: list.error.message })}</p>

  const all = list.data ?? []
  const groups = meta.data?.groups ?? []
  const groupName = (id?: string) => (id ? groups.find((g) => g.id === id)?.name ?? id : t('list.group.none'))
  const groupOf = (a: ApiRow) => (a.group && groups.some((g) => g.id === a.group) ? a.group : NO_GROUP)

  // Within a facet, values are alternatives (OR); facets, search and state combine (AND).
  const q = query.trim().toLowerCase()
  const bySearch = (a: ApiRow) =>
    !q || [a.id, a.name, a.owner ?? '', a.title ?? '', a.group ? groupName(a.group) : '', ...a.tags].some((s) => s.toLowerCase().includes(q))
  const byGroups = (a: ApiRow) => !groupSel.length || groupSel.includes(groupOf(a))
  const byTags = (a: ApiRow) => !tagSel.length || a.tags.some((x) => tagSel.includes(x))
  const scoped = all.filter((a) => bySearch(a) && byGroups(a) && byTags(a))
  const rows = scoped.filter(filters[filter]).sort((a, b) => {
    const c = sorters[sortKey].cmp(a, b) || collator.compare(a.name, b.name)
    return dir === 'asc' ? c : -c
  })
  const count = (f: Filter) => scoped.filter(filters[f]).length

  // Facet counts ignore the facet's own selection, so that every option shows what it would add.
  const groupBase = all.filter((a) => bySearch(a) && byTags(a) && filters[filter](a))
  const tagBase = all.filter((a) => bySearch(a) && byGroups(a) && filters[filter](a))
  const groupOptions: FacetOption[] = [
    ...groups.map((g) => ({ value: g.id, label: g.name, count: groupBase.filter((a) => groupOf(a) === g.id).length })),
    ...(all.some((a) => groupOf(a) === NO_GROUP) && groups.length
      ? [{ value: NO_GROUP, label: t('list.group.none'), count: groupBase.filter((a) => groupOf(a) === NO_GROUP).length }]
      : []),
  ]
  const tagOptions: FacetOption[] = (meta.data?.tags ?? []).map((x) => ({
    value: x,
    label: `#${x}`,
    count: tagBase.filter((a) => a.tags.includes(x)).length,
  }))
  const active = [
    ...groupSel.map((g) => ({ key: 'group', value: g, label: g === NO_GROUP ? t('list.group.none') : groupName(g) })),
    ...tagSel.map((x) => ({ key: 'tag', value: x, label: `#${x}` })),
  ]

  const sections: Section[] = byGroup ? sectionize(rows, groups, groupOf, t('list.group.none')) : [{ key: '', title: '', rows }]

  // Only displayed rows can be acted upon: a hidden selected row is never touched by surprise.
  const selected = rows.filter((a) => selection.has(a.id))
  const toAck = selected.filter((a) => a.unacknowledged > 0)
  const setSelected = (ids: string[], on: boolean) =>
    setSelection((prev) => {
      const next = new Set(prev)
      ids.forEach((id) => (on ? next.add(id) : next.delete(id)))
      return next
    })
  const running = bulk?.kind === 'running'

  async function runBulk(ids: string[], label: 'bulk.progress' | 'bulk.ackProgress', one: (id: string) => Promise<unknown>) {
    let done = 0
    const results: { id: string; ok: boolean; value?: unknown; error?: string }[] = []
    setBulk({ kind: 'running', label, done, total: ids.length })
    const queue = [...ids]
    const worker = async () => {
      for (let id = queue.shift(); id !== undefined; id = queue.shift()) {
        try {
          results.push({ id, ok: true, value: await one(id) })
        } catch (e) {
          results.push({ id, ok: false, error: `${id} — ${(e as Error).message}` })
        }
        setBulk({ kind: 'running', label, done: ++done, total: ids.length })
      }
    }
    await Promise.all(Array.from({ length: Math.min(BULK_CONCURRENCY, ids.length) }, worker))
    await qc.invalidateQueries()
    return results
  }

  const verify = async () => {
    const res = await runBulk(selected.map((a) => a.id), 'bulk.progress', (id) => checkOne.mutateAsync(id))
    const outcome = (s: string) => res.filter((r) => r.ok && (r.value as { status: string }).status === s).length
    setBulk({
      kind: 'done',
      message: t('bulk.checked', { n: res.length, changed: outcome('changed'), errors: outcome('error') + res.filter((r) => !r.ok).length }),
      failures: res.flatMap((r) => (r.error ? [r.error] : [])),
    })
  }
  const acknowledge = async () => {
    const res = await runBulk(toAck.map((a) => a.id), 'bulk.ackProgress', (id) => ackOne.mutateAsync(id))
    setBulk({
      kind: 'done',
      message: t('bulk.acked', { n: res.filter((r) => r.ok).length }),
      failures: res.flatMap((r) => (r.error ? [r.error] : [])),
    })
  }

  const allIds = rows.map((a) => a.id)

  return (
    <>
      <div className="toolbar">
        <h1>{t('list.title')}</h1>
        <div className="toolbar-controls">
          {groupOptions.length > 0 && (
            <FacetFilter label={t('list.facet.groups')} options={groupOptions} selected={groupSel} onChange={(v) => setAll('group', v)} />
          )}
          {tagOptions.length > 0 && (
            <FacetFilter label={t('list.facet.tags')} options={tagOptions} selected={tagSel} onChange={(v) => setAll('tag', v)} />
          )}
          <input className="search" type="search" placeholder={t('list.search')} value={query}
            onChange={(e) => set('q', e.target.value)} aria-label={t('list.search')} />
        </div>
      </div>

      <div className="chips" role="tablist">
        {(Object.keys(filters) as Filter[]).map((f) => (
          <button key={f} role="tab" aria-selected={filter === f} className={`chip ${filter === f ? 'active' : ''} ${f === 'pending' ? 'chip-pending' : ''}`}
            onClick={() => set('state', f === 'all' ? '' : f)}>
            {t(`list.filter.${f}`)} <span className="chip-count">{count(f)}</span>
          </button>
        ))}
        <span className="spacer" />
        {groups.length > 0 && (
          <Segmented label={t('list.view')} value={byGroup ? 'group' : 'alpha'} onChange={(v) => set('by', v === 'group' ? 'group' : '')}
            options={[{ value: 'alpha', label: t('list.view.alpha') }, { value: 'group', label: t('list.view.groups') }]} />
        )}
      </div>

      {active.length > 0 && (
        <div className="active-filters">
          {active.map((f) => (
            <button key={`${f.key}:${f.value}`} className="chip active" aria-label={t('list.facet.remove', { x: f.label })}
              onClick={() => setAll(f.key, params.getAll(f.key).filter((v) => v !== f.value))}>
              {f.key === 'group' && <span className="muted">{t('detail.group')} : </span>}
              {f.label} <span aria-hidden>✕</span>
            </button>
          ))}
          <button className="link small" onClick={() => update((p) => (p.delete('group'), p.delete('tag')))}>{t('list.facet.clear')}</button>
        </div>
      )}

      {rows.length === 0 ? (
        <p className="muted">{t('list.empty')}</p>
      ) : (
        <div className="table-wrap">
          <table className="table api-table">
            <colgroup>
              <col className="c-select" />
              <col />
              <col className="c-status" />
              <col className="c-version" />
              <col className="c-checked" />
              <col className="c-changes" />
              <col className="c-action" />
            </colgroup>
            <thead>
              <tr>
                <th className="select-cell">
                  <TriCheckbox label={t('bulk.selectAll')} checked={selected.length > 0 && selected.length === rows.length}
                    indeterminate={selected.length > 0 && selected.length < rows.length} onChange={(on) => setSelected(allIds, on)} />
                </th>
                <SortTh k="name" label={t('col.api')} sortKey={sortKey} dir={dir} onSort={sortBy} />
                <SortTh k="status" label={t('col.status')} sortKey={sortKey} dir={dir} onSort={sortBy} />
                <SortTh k="version" label={t('col.version')} sortKey={sortKey} dir={dir} onSort={sortBy} />
                <SortTh k="checked" label={t('col.lastCheck')} sortKey={sortKey} dir={dir} onSort={sortBy} />
                <SortTh k="changes" label={t('col.changes')} sortKey={sortKey} dir={dir} onSort={sortBy} />
                <th aria-hidden />
              </tr>
            </thead>
            <tbody>
              {sections.map((s) => {
                const ids = s.rows.map((a) => a.id)
                const n = ids.filter((id) => selection.has(id)).length
                return (
                  <Fragment key={s.key}>
                    {byGroup && (
                      <tr className="section-row">
                        <th className="select-cell">
                          <TriCheckbox label={t('bulk.selectGroup', { name: s.title })} checked={n === ids.length}
                            indeterminate={n > 0 && n < ids.length} onChange={(on) => setSelected(ids, on)} />
                        </th>
                        <th colSpan={6}>
                          {s.title} <span className="muted small">· {s.rows.length}</span>
                          {s.description && <div className="small muted section-desc">{s.description}</div>}
                        </th>
                      </tr>
                    )}
                    {s.rows.map((a) => (
                      <Row key={a.id} a={a} showGroup={!byGroup} groupName={groupName(a.group)}
                        selected={selection.has(a.id)} onSelect={(on) => setSelected([a.id], on)}
                        onGroup={() => addTo('group', groupOf(a))} onTag={(x) => addTo('tag', x)} />
                    ))}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {(selected.length > 0 || bulk) && (
        <div className="bulkbar" role="region" aria-label={t('bulk.selected', { n: selected.length })} aria-live="polite">
          <strong>{t('bulk.selected', { n: selected.length })}</strong>
          <button className="btn" disabled={running || selected.length === 0} onClick={verify}>{t('bulk.check', { n: selected.length })}</button>
          <button className="btn btn-secondary" disabled={running || toAck.length === 0} onClick={acknowledge}>{t('bulk.ack', { n: toAck.length })}</button>
          <button className="btn btn-ghost" disabled={running} onClick={() => (setSelection(new Set()), setBulk(null))}>{t('bulk.clear')}</button>
          {bulk?.kind === 'running' && <span className="small">{t(bulk.label, { done: bulk.done, total: bulk.total })}</span>}
          {bulk?.kind === 'done' && (
            <span className="small bulk-result">
              {bulk.message}
              {bulk.failures.length > 0 && (
                <span className="bulk-fail" title={bulk.failures.join('\n')}> {t('bulk.failed', { n: bulk.failures.length, msg: bulk.failures[0] })}</span>
              )}
              <button className="link" onClick={() => setBulk(null)} aria-label={t('bulk.dismiss')}> ✕</button>
            </span>
          )}
        </div>
      )}
    </>
  )
}

function SortTh({ k, label, sortKey, dir, onSort }: { k: SortKey; label: string; sortKey: SortKey; dir: Dir; onSort: (k: SortKey) => void }) {
  const { t } = useI18n()
  const on = k === sortKey
  return (
    <th aria-sort={on ? (dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
      <button type="button" className={`sort ${on ? 'on' : ''}`} onClick={() => onSort(k)} title={t('list.sortBy', { col: label })}>
        {label}
        <span aria-hidden className="sort-icon">{on ? (dir === 'asc' ? '▲' : '▼') : '↕'}</span>
      </button>
    </th>
  )
}

function Row({ a, showGroup, groupName, selected, onSelect, onGroup, onTag }: {
  a: ApiRow
  showGroup: boolean
  groupName: string
  selected: boolean
  onSelect: (on: boolean) => void
  onGroup: () => void
  onTag: (t: string) => void
}) {
  const { t, relative, date } = useI18n()
  return (
    <tr className={`${a.unacknowledged_breaking > 0 ? 'row-alert' : ''} ${selected ? 'row-selected' : ''}`}>
      <td className="select-cell">
        <input type="checkbox" checked={selected} onChange={(e) => onSelect(e.target.checked)} aria-label={t('bulk.selectRow', { name: a.name })} />
      </td>
      <td>
        <Link to={`/apis/${a.id}`} className="api-name">{a.name}</Link>
        <div className="small muted ellipsis">{[a.owner, a.id].filter(Boolean).join(' · ')}</div>
        {(showGroup && a.group) || a.tags.length ? (
          <span className="tags">
            {showGroup && a.group && <button className="tag tag-group" onClick={onGroup}>{groupName}</button>}
            {a.tags.map((x) => <button key={x} className="tag" onClick={() => onTag(x)}>#{x}</button>)}
          </span>
        ) : null}
      </td>
      <td><StatusBadge status={a.status} /></td>
      <td className="mono ellipsis" title={a.version}>{a.version || '—'}</td>
      <td title={date(a.last_checked_at)}>{relative(a.last_checked_at)}</td>
      <td>
        {a.unacknowledged > 0 ? (
          <Link to={`/apis/${a.id}`} className="badge pending">
            {t(a.unacknowledged > 1 ? 'badge.pending.other' : 'badge.pending.one', { n: a.unacknowledged })}
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

function sectionize(rows: ApiRow[], groups: Group[], groupOf: (a: ApiRow) => string, noGroup: string): Section[] {
  const sections: Section[] = groups
    .map((g) => ({ key: g.id, title: g.name, description: g.description, rows: rows.filter((a) => groupOf(a) === g.id) }))
    .filter((s) => s.rows.length > 0)
  const orphans = rows.filter((a) => groupOf(a) === NO_GROUP)
  if (orphans.length) sections.push({ key: NO_GROUP, title: noGroup, rows: orphans })
  return sections
}
