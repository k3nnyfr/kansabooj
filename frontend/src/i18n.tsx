import { createContext, useContext, useMemo, useState, type ReactNode } from 'react'

export type Lang = 'fr' | 'en'

const fr = {
  'nav.apis': 'APIs',
  'nav.status': 'Statut',
  'lang.switch': 'English',
  'list.title': 'APIs surveillées',
  'list.search': 'Rechercher une API, une équipe…',
  'list.filter.all': 'Toutes',
  'list.filter.unseen': 'Non vues',
  'list.filter.breaking': 'Cassantes',
  'list.filter.errors': 'En erreur',
  'list.empty': 'Aucune API ne correspond.',
  'list.group.all': 'Tous les groupes',
  'list.group.none': 'Sans groupe',
  'list.groupBy': 'Regrouper par groupe',
  'list.tags': 'Tags',
  'list.clearTag': 'Retirer le filtre de tag {tag}',
  'detail.group': 'Groupe',
  'detail.resolved': 'Spec suivie',
  'detail.degraded': 'Mode dégradé : la page ne publie pas de spec exploitable, seul son texte est comparé (pas d’analyse du contrat).',
  'change.degraded': 'mode dégradé',
  'check.discovery': 'Spec trouvée via {how}',
  'detail.network': 'Profil réseau',
  'status.instance': 'Instance',
  'status.role': 'Rôle',
  'status.leader': 'leader (planifie les checks)',
  'status.follower': 'secondaire (sert l’interface et les checks manuels)',
  'col.api': 'API',
  'col.status': 'État',
  'col.version': 'Version',
  'col.lastCheck': 'Dernier check',
  'col.changes': 'Évolutions',
  'status.pending': 'En attente',
  'status.ok': 'À jour',
  'status.error': 'Erreur',
  'status.auth_error': 'Accès refusé',
  'status.stale': 'Injoignable',
  'badge.unseen': '{n} non vue(s)',
  'badge.breaking': 'cassant',
  'action.check': 'Vérifier',
  'action.checking': 'Vérification…',
  'action.ackAll': 'Tout marquer comme vu',
  'action.ack': 'Marquer comme vu',
  'action.showOut': 'Afficher les changements hors périmètre ({n})',
  'action.hideOut': 'Masquer les changements hors périmètre',
  'action.loadMore': 'Voir plus ancien',
  'check.unchanged': 'Inchangé',
  'check.notModified': 'Inchangé (304)',
  'check.changed': 'Nouvelle évolution détectée',
  'check.error': 'Échec',
  'detail.source': 'Source',
  'detail.operations': '{n} opérations',
  'detail.interval': 'Vérifiée toutes les {d}',
  'detail.scope': 'Périmètre réexposé',
  'detail.scope.all': 'Non déclaré : tous les changements sont considérés dans le périmètre.',
  'detail.timeline': 'Évolutions',
  'detail.noChanges': 'Aucune évolution enregistrée. Le premier check aura lieu sous peu.',
  'detail.checks': 'Derniers checks',
  'detail.spec': 'Spec normalisée',
  'detail.specRaw': 'Spec brute',
  'change.first': 'Première capture',
  'change.version': 'Version {from} → {to}',
  'change.sameVersion': 'Version inchangée ({v})',
  'change.docChanged': 'Documentation modifiée',
  'change.seenBy': 'Vu par {who}, {when}',
  'change.new': 'Nouveau',
  'change.noInScope': 'Aucun changement dans le périmètre réexposé.',
  'level.error': 'Cassant',
  'level.warn': 'À surveiller',
  'level.info': 'Info',
  'level.outOfScope': 'hors périmètre',
  'errkind.auth': 'Les identifiants sont refusés par le partenaire : vérifier la section auth et les secrets.',
  'errkind.network': 'Source injoignable (réseau, DNS, proxy ou timeout).',
  'errkind.http': 'Le serveur du partenaire a répondu en erreur.',
  'errkind.denied': 'URL bloquée par la politique réseau (http.deny_cidrs).',
  'errkind.too_large': 'Document trop volumineux (http.max_body_bytes).',
  'errkind.invalid_spec': "Le document récupéré n'est pas une spécification OpenAPI / Swagger exploitable.",
  'errkind.unsupported': 'Type de source pas encore supporté.',
  'errkind.config': 'Configuration invalide pour cette API.',
  'errkind.other': 'Erreur lors de la vérification.',
  'errors.consecutive': '{n} échec(s) consécutif(s)',
  'status.title': 'Statut du service',
  'status.scheduler': 'Planificateur',
  'status.lastTick': 'Dernier passage',
  'status.configAt': 'Configuration chargée',
  'status.running': 'Checks en cours',
  'status.none': 'aucun',
  'status.failing': 'APIs en échec',
  'status.allGood': 'Toutes les APIs répondent correctement.',
  'status.next': 'Prochains checks',
  'trigger.schedule': 'planifié',
  'trigger.manual': 'manuel',
  'trigger.startup': 'démarrage',
  'never': 'jamais',
  'loading': 'Chargement…',
  'error.load': 'Impossible de charger les données : {msg}',
  'notFound': 'API introuvable.',
  'back': '← Toutes les APIs',
}

type Key = keyof typeof fr

const en: Record<Key, string> = {
  'nav.apis': 'APIs',
  'nav.status': 'Status',
  'lang.switch': 'Français',
  'list.title': 'Monitored APIs',
  'list.search': 'Search an API, a team…',
  'list.filter.all': 'All',
  'list.filter.unseen': 'Unseen',
  'list.filter.breaking': 'Breaking',
  'list.filter.errors': 'Failing',
  'list.empty': 'No API matches.',
  'list.group.all': 'All groups',
  'list.group.none': 'No group',
  'list.groupBy': 'Group by group',
  'list.tags': 'Tags',
  'list.clearTag': 'Remove tag filter {tag}',
  'detail.group': 'Group',
  'detail.resolved': 'Tracked spec',
  'detail.degraded': 'Degraded mode: the page publishes no usable spec, only its text is compared (no contract analysis).',
  'change.degraded': 'degraded mode',
  'check.discovery': 'Spec found via {how}',
  'detail.network': 'Network profile',
  'status.instance': 'Instance',
  'status.role': 'Role',
  'status.leader': 'leader (schedules checks)',
  'status.follower': 'secondary (serves the UI and manual checks)',
  'col.api': 'API',
  'col.status': 'State',
  'col.version': 'Version',
  'col.lastCheck': 'Last check',
  'col.changes': 'Changes',
  'status.pending': 'Pending',
  'status.ok': 'Up to date',
  'status.error': 'Error',
  'status.auth_error': 'Access denied',
  'status.stale': 'Unreachable',
  'badge.unseen': '{n} unseen',
  'badge.breaking': 'breaking',
  'action.check': 'Check',
  'action.checking': 'Checking…',
  'action.ackAll': 'Mark all as seen',
  'action.ack': 'Mark as seen',
  'action.showOut': 'Show out-of-scope changes ({n})',
  'action.hideOut': 'Hide out-of-scope changes',
  'action.loadMore': 'Load older',
  'check.unchanged': 'Unchanged',
  'check.notModified': 'Unchanged (304)',
  'check.changed': 'New change detected',
  'check.error': 'Failed',
  'detail.source': 'Source',
  'detail.operations': '{n} operations',
  'detail.interval': 'Checked every {d}',
  'detail.scope': 'Re-exposed scope',
  'detail.scope.all': 'Not declared: every change is considered in scope.',
  'detail.timeline': 'Changes',
  'detail.noChanges': 'No change recorded yet. The first check will run shortly.',
  'detail.checks': 'Recent checks',
  'detail.spec': 'Normalized spec',
  'detail.specRaw': 'Raw spec',
  'change.first': 'First capture',
  'change.version': 'Version {from} → {to}',
  'change.sameVersion': 'Version unchanged ({v})',
  'change.docChanged': 'Documentation changed',
  'change.seenBy': 'Seen by {who}, {when}',
  'change.new': 'New',
  'change.noInScope': 'No change in the re-exposed scope.',
  'level.error': 'Breaking',
  'level.warn': 'Review',
  'level.info': 'Info',
  'level.outOfScope': 'out of scope',
  'errkind.auth': 'Credentials are rejected by the partner: check the auth section and secrets.',
  'errkind.network': 'Source unreachable (network, DNS, proxy or timeout).',
  'errkind.http': "The partner's server returned an error.",
  'errkind.denied': 'URL blocked by the network policy (http.deny_cidrs).',
  'errkind.too_large': 'Document too large (http.max_body_bytes).',
  'errkind.invalid_spec': 'The fetched document is not a usable OpenAPI / Swagger specification.',
  'errkind.unsupported': 'Source type not supported yet.',
  'errkind.config': 'Invalid configuration for this API.',
  'errkind.other': 'Error while checking.',
  'errors.consecutive': '{n} consecutive failure(s)',
  'status.title': 'Service status',
  'status.scheduler': 'Scheduler',
  'status.lastTick': 'Last run',
  'status.configAt': 'Configuration loaded',
  'status.running': 'Running checks',
  'status.none': 'none',
  'status.failing': 'Failing APIs',
  'status.allGood': 'All APIs respond correctly.',
  'status.next': 'Next checks',
  'trigger.schedule': 'scheduled',
  'trigger.manual': 'manual',
  'trigger.startup': 'startup',
  never: 'never',
  loading: 'Loading…',
  'error.load': 'Could not load data: {msg}',
  notFound: 'API not found.',
  back: '← All APIs',
}

const dictionaries: Record<Lang, Record<Key, string>> = { fr, en }
const STORAGE_KEY = 'kansabooj.lang'

type I18n = {
  lang: Lang
  setLang: (l: Lang) => void
  t: (key: Key, vars?: Record<string, string | number>) => string
  date: (iso?: string) => string
  relative: (iso?: string) => string
}

const Ctx = createContext<I18n | null>(null)

function storedLang(): Lang | null {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    return v === 'fr' || v === 'en' ? v : null
  } catch {
    return null
  }
}

export function I18nProvider({ serverDefault, children }: { serverDefault?: Lang; children: ReactNode }) {
  const [chosen, setChosen] = useState<Lang | null>(storedLang)
  const lang: Lang = chosen ?? serverDefault ?? 'fr'

  const value = useMemo<I18n>(() => {
    const dict = dictionaries[lang]
    const rtf = new Intl.RelativeTimeFormat(lang, { numeric: 'auto' })
    const dtf = new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short' })
    return {
      lang,
      setLang: (l) => {
        try {
          localStorage.setItem(STORAGE_KEY, l)
        } catch {
          /* private mode: choice lasts for the session */
        }
        setChosen(l)
      },
      t: (key, vars) => {
        let s = dict[key] ?? key
        if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v))
        return s
      },
      date: (iso) => (iso ? dtf.format(new Date(iso)) : dict.never),
      relative: (iso) => {
        if (!iso) return dict.never
        const diff = (new Date(iso).getTime() - Date.now()) / 1000
        const abs = Math.abs(diff)
        if (abs < 60) return rtf.format(Math.round(diff), 'second')
        if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute')
        if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour')
        return rtf.format(Math.round(diff / 86400), 'day')
      },
    }
  }, [lang])

  document.documentElement.lang = lang
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useI18n(): I18n {
  const v = useContext(Ctx)
  if (!v) throw new Error('useI18n outside I18nProvider')
  return v
}
