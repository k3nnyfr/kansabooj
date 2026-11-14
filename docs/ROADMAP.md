# Roadmap

## v0.1 — Walking skeleton (CLI) ✅ 2026-11-14
- [x] `config` : chargement YAML, `${ENV}` / `${file:...}`, defaults, validation (ids uniques, notifiers référencés existants)
- [x] `fetch` : client HTTP (proxy entreprise, CA, timeout, deny_cidrs, ETag)
- [x] `fetch` : authentification `header` | `query` | `basic` | `bearer` | `oauth2_client_credentials` | `mtls`, credentials limités à l'origine configurée
- [x] `normalize` : OAS 3.x + Swagger 2.0 → forme canonique + hash
- [x] `diff` : wrapper oasdiff → changements classés + `in_scope`
- [x] `store` : migrations, blobs content-addressed, sync YAML → `apis`, snapshots/changes
- [x] CLI `kansabooj validate`, `kansabooj check <id>`
- [x] `config/apis.schema.json` (JSON Schema pour l'autocomplétion IDE)

## v0.2 — Serveur, front & notifications ✅ 2026-11-14
- [x] Commande `serve` : scheduler + API REST + front embarqué, reload de la config (SIGHUP / endpoint admin)
- [x] `scheduler` : interval + jitter, check manuel, statuts `error` / `auth_error` / `stale`
- [x] API REST `/api/*`
- [x] Front : liste, timeline, détail d'un changement, statut
- [x] Notifiers `googlechat`, `slack`, `teams`, `generic` (HMAC) avec retry/backoff
- [x] Acquittement des changements (header SSO)
- [x] Notifications `stale` / `recovered`
- [x] Langue **français / anglais** : langue par défaut (`server.language`), sélecteur dans le front, langue par notifier

## v0.3 — Groupes, tags, APIs internes & Kubernetes ✅ 2026-11-14
- [x] **PostgreSQL** en production (SQLite conservé en local / CLI), suite de tests exécutable sur les deux (ADR 0004)
- [x] Multi-réplicas : leader élu par verrou PostgreSQL, verrou par API, enregistrement optimiste des versions
- [x] Conteneur sans état : config par variables d'environnement, `/readyz`, rechargement à chaud ConfigMap / Secrets
- [x] Image (Containerfile multi-stage, non-root, compatible UID arbitraire OpenShift), manifests kustomize
      Kubernetes (Ingress) / OpenShift (Route), exemple CloudNativePG, compose local
- [x] **Groupes d'API** (`groups:`) : regroupement (partenaire, domaine, interne/externe…) portant des valeurs par défaut
      héritées par les APIs du groupe (owner, interval, notify, auth, profil réseau, ignore)
- [x] **Tags** libres par API (`tags: [paiement, pci, critique]`)
- [x] Front : filtres et vue regroupée par groupe / tag ; API REST filtrable (`?group=&tag=`)
- [x] Routage des notifications par groupe / tag (`notifiers[].subscribe`)
- [x] **Profils réseau** (`http.profiles`) pour les APIs internes : sans proxy, plages internes autorisées
      (`allow_cidrs`), CA dédiée — sans affaiblir le profil « internet » par défaut
- [x] JSON Schema + `validate` à jour (groupe inexistant, héritage)

## v0.4 — PostgreSQL tiers & mode API / as-code (ADR 0003) ✅ 2026-11-14
- [x] Section `database:` : serveur PostgreSQL tiers (TLS verify-full, certificat client, schéma dédié, pool),
      `migrate: false` + commande `kansabooj migrate` (comptes séparés DBA), migrations sérialisées entre réplicas,
      composant kustomize `external-db` + Job de migration, guide `docs/guides/postgresql-externe.md`
- [x] Configuration multi-fichiers `include:` (un fragment `apis.d/<équipe>.yml` par équipe) + schéma de fragment
- [x] Spec OpenAPI de l'API Kansabooj servie sur `/api/openapi.yaml` (test : chaque route est décrite)
- [x] `POST /api/config/validate?fragment=…` pour valider une MR de config depuis la CI
- [x] Jetons d'API à portée limitée (`read`, `check`, `ack`, `validate`, `admin`), `require_identity`, `admins`
- [x] `kansabooj diff` (fichiers, URLs, `kansabooj:<api-id>`) + guide « Kansabooj dans la CI »

## v0.5 — Découverte, observabilité, CI ✅ 2026-11-14
- [x] `discover` : Swagger-UI (initializer, `url` / `urls` / `configUrl`, variable JS, URL calculée), springdoc,
      Redoc, Swashbuckle et emplacements usuels ; URL mémorisée et réutilisée, redécouverte si elle disparaît ; `spec_name`
- [x] Mode dégradé `html` (texte extrait, `text_ignore`, diff ligne à ligne) ; type `auto` avec repli poli
- [x] Specs multi-fichiers (`$ref` externes OpenAPI 3, http(s) uniquement, via la politique réseau de l'API)
- [x] `/metrics` Prometheus + guide d'exploitation (alertes suggérées)
- [x] CI Gitea Actions : tests SQLite + PostgreSQL, front, schémas, kustomize, image + scan Trivy + push registre
- [x] Composant NetworkPolicy

## v0.6 — Lecture des évolutions, récapitulatifs ✅ 2026-11-14
- [x] Comparaison libre de deux versions dans l'interface (changements + vue côte à côte des specs normalisées),
      route `GET /api/apis/{id}/compare` ; lien « Voir le diff » sur chaque évolution
- [x] Récapitulatif quotidien / hebdomadaire par notifier (`digest`), avec envoi immédiat optionnel des ruptures
      (`immediate_level`)
- [x] Libellés des changements en français (catalogue de 825 messages oasdiff, couverture vérifiée par un test)
- [x] `$ref` externes en Swagger 2.0 ; `kansabooj diff` sur des specs multi-fichiers locales

## v0.7 — Ergonomie de la liste ✅ 2026-11-14
- [x] Filtres groupes et tags unifiés, sélection multiple (OU dans une facette, ET entre facettes), compteurs par option
- [x] Bascule « Alphabétique / Groupes », tri sur toutes les colonnes, en-têtes fixes au défilement
- [x] Actions groupées : vérifier maintenant, marquer comme vu (lignes affichées seulement, 4 en parallèle)
- [x] « Changements en attente » (bleu), bouton « Vérifier maintenant » et colonnes à largeur fixe

## v0.8 — Documentation des versions ✅ 2026-11-14
- [x] Rendu Swagger UI de la spec normalisée sous la timeline (dernière version, ou une version de la timeline via
      `?doc=`) : chargé à la demande, thème de l'interface (clair et sombre), en lecture seule (« Try it out » désactivé)
- [x] Page élargie (1600 px) : les tags de la liste tiennent sur une ligne

## v0.9 — Journal et thème ✅ 2026-11-14
- [x] Onglet « Journal » : checks manuels (auteur enregistré, migration 0006) et planifiés, acquittements,
      notifications ; filtres par type et par API, pagination qui ne sépare pas les entrées simultanées.
      Lu dans les tables existantes (`GET /api/journal`), donc soumis à la même rétention que les checks
- [x] Bouton de thème clair / sombre (suit le système tant qu'aucun choix n'est mémorisé)
- [x] Captures du README en 1600 px, avec le journal et le thème sombre

## Plus tard (selon retours)
- Chart Helm (si les équipes d'exploitation le préfèrent à kustomize)
- CLI et messages d'erreur détaillés bilingues (aujourd'hui en français ; le front traduit déjà les catégories d'erreur)
- `gateway.published_spec` : périmètre déduit du contrat publié + détection de drift amont ↔ publié (agnostique de la Gateway)
- Autres formats : AsyncAPI, GraphQL SDL, WSDL (interface `Differ` par format)
- UI admin qui ouvre une MR sur le dépôt de config (plutôt qu'écrire en base)
