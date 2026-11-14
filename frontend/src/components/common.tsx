import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, type ApiStatus, type CheckResult, type Level } from '../api/client'
import { useI18n } from '../i18n'

export function StatusBadge({ status }: { status: ApiStatus }) {
  const { t } = useI18n()
  return <span className={`badge status-${status}`}>{t(`status.${status}`)}</span>
}

export function LevelBadge({ level }: { level: Level }) {
  const { t } = useI18n()
  return <span className={`badge level-${level}`}>{t(`level.${level}`)}</span>
}

export function Counts({ errors, warns, infos }: { errors: number; warns: number; infos: number }) {
  const { t } = useI18n()
  return (
    <span className="counts">
      {errors > 0 && <span className="badge level-error">{errors} {t('level.error')}</span>}
      {warns > 0 && <span className="badge level-warn">{warns} {t('level.warn')}</span>}
      {infos > 0 && <span className="badge level-info">{infos} {t('level.info')}</span>}
    </span>
  )
}

const knownKinds = ['auth', 'network', 'http', 'denied', 'too_large', 'invalid_spec', 'unsupported', 'config']

export function ErrorHint({ kind, detail, consecutive }: { kind?: string; detail?: string; consecutive?: number }) {
  const { t } = useI18n()
  const key = (kind && knownKinds.includes(kind) ? `errkind.${kind}` : 'errkind.other') as Parameters<typeof t>[0]
  return (
    <div className="callout callout-error" role="alert">
      <strong>{t(key)}</strong>
      {detail && <div className="mono small">{detail}</div>}
      {!!consecutive && <div className="small muted">{t('errors.consecutive', { n: consecutive })}</div>}
    </div>
  )
}

/** Button triggering a manual check; invalidates cached data afterwards. */
export function CheckButton({ id, onResult }: { id: string; onResult?: (r: CheckResult) => void }) {
  const { t } = useI18n()
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: () => api.check(id),
    onSuccess: (r) => {
      onResult?.(r)
      qc.invalidateQueries()
    },
  })
  return (
    <button className="btn" onClick={() => m.mutate()} disabled={m.isPending}>
      {m.isPending ? t('action.checking') : t('action.check')}
    </button>
  )
}
