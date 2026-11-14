# CLAUDE.md — Kansabooj

Outil interne de **surveillance des contrats d'API partenaires** réexposées dans l'API Gateway de l'entreprise :
check périodique des specs (OpenAPI / Swagger / Swagger-UI / Redoc), historisation des versions, timeline des évolutions,
notifications webhooks (Google Chat, Slack, Teams, générique).

Architecture, décisions et challenge du besoin : **`docs/ARCHITECTURE.md`** (à lire avant toute évolution structurante).
Roadmap : `docs/ROADMAP.md`. Décisions d'architecture : `docs/adr/`.

## Stack

- **Backend** : Go (1.26, cf. `backend/go.mod`), binaire unique `kansabooj` qui embarque le front (`go:embed`).
  - `github.com/oasdiff/oasdiff` — diff sémantique + classification breaking (ERR/WARN/INFO). **Ne pas réécrire de diff maison.**
  - `github.com/getkin/kin-openapi` — parsing OAS 3.x, conversion Swagger 2.0 → OAS 3 (`openapi2conv`), résolution `$ref`.
  - `github.com/jackc/pgx/v5` (PostgreSQL, production) et `modernc.org/sqlite` (local/CLI, pure Go, pas de CGO),
    `log/slog`, `net/http` (router standard Go 1.22+).
- **Front** : React + Vite + TypeScript, TanStack Query, React Router. Build dans `frontend/dist`, copié/embarqué par le backend.
- **Stockage** : PostgreSQL en production, SQLite en local (ADR 0004). Migrations dans
  `backend/internal/store/migrations/{postgres,sqlite}/`, **même numéro dans les deux dossiers**, jamais modifiées une fois mergées.
- **Déploiement** : Kubernetes / OpenShift (`deploy/kubernetes`, kustomize), image `Containerfile`.

## Arborescence

```
backend/
  cmd/kansabooj/        CLI : validate | check | history | serve | migrate | diff | token
  internal/config/      chargement + validation de apis.yml, résolution ${ENV}, fusion defaults
  internal/discover/    page de doc (Swagger-UI, Redoc, springdoc…) → URL de spec ; scripts suivis seulement sur le même hôte
  internal/fetch/       client HTTP : proxy, CA, auth, ETag, deny_cidrs, timeouts
  internal/normalize/   parse → v2→v3 → bundle $ref externes → ignore rules → JSON canonique → sha256 ; html.go : mode dégradé
  internal/diff/        wrapper oasdiff → []ChangeItem (+ calcul in_scope vs exposed_operations)
  internal/store/       accès base (PostgreSQL / SQLite, dialect.go), migrations, blobs, verrous distribués
  internal/store/storetest/  base jetable pour les tests (SQLite, ou PostgreSQL si KANSABOOJ_TEST_POSTGRES)
  internal/monitor/     orchestration d'un check : fetch → normalize → diff → store (utilisé par CLI et scheduler)
  internal/scheduler/   boucle de checks (interval + jitter, 1 fetch concurrent par domaine), reload de config
  internal/notify/      décide/rend/envoie : slack | googlechat | teams | generic (HMAC), idempotent, retry/backoff
  internal/i18n/        catalogue FR/EN côté serveur (notifications)
  internal/metrics/     métriques Prometheus (compteurs d'instance + collecteur par API lu en base)
  internal/httpapi/     API REST /api/* (table routes() avec scope par route), auth (jetons/SSO), validation de config,
                        openapi.yaml embarqué, front embarqué (web/dist, sinon web/placeholder.html), /healthz /readyz
  internal/token/       jetons d'API (kbj_…), seul le hash sha256 est configuré
frontend/src/
  pages/                ApiList, ApiDetail (timeline + détail des changements), StatusPage
  components/           badges, bouton de check, encart d'erreur
  api/client.ts         client typé de l'API REST — à garder aligné avec internal/httpapi et internal/store
  i18n.tsx              catalogue FR/EN du front + formatage des dates (Intl)
config/apis.schema.json JSON Schema de la config (autocomplétion IDE) — à tenir à jour avec internal/config
config/apis.fragment.schema.json  schéma des fragments d'équipe (apis.d/*.yml : groups + apis uniquement)
config/apis.example.yml exemple exhaustif (toutes les options, secrets fictifs : ne s'exécute pas tel quel)
config/apis.demo.yml    config exécutable sans secret (Worldline préprod + Petstore), défaut du Taskfile
deploy/kubernetes/      base + overlays kubernetes (Ingress) / openshift (Route), exemple CloudNativePG
deploy/compose.yaml     essai local avec PostgreSQL
.gitea/workflows/ci.yml CI Gitea Actions (rejouable localement : task ci [PG=postgres://…])
scripts/check-schemas.py  validation JSON Schema des configs du dépôt
docs/guides/            postgresql-externe, ci, exploitation (métriques, alertes)
deploy/                 Dockerfile, compose, manifests
docs/                   ARCHITECTURE.md, ROADMAP.md, adr/
```

## Commandes

Pas de Makefile : tout passe par [Task](https://taskfile.dev) (`Taskfile.yml`, `task --list`).

```bash
task validate                        # valide config/apis.demo.yml (CONFIG=... pour une autre)
task check -- worldline-direct       # check réel, persiste dans data/kansabooj.db ; -- --all ; -- --dry-run <id>
task history -- worldline-direct
task serve                           # API + scheduler sur :8080
task ui:dev                          # front Vite sur :5173 (proxy /api → :8080)
task test                            # go test -race ./... (SQLite) + typecheck du front
task test:pg PG=postgres://…         # même suite sur PostgreSQL (base temporaire par test)
task lint                            # gofmt, go vet, tsc
task build                           # front embarqué + binaire bin/kansabooj (version = git describe)
task image                           # image de conteneur (podman ou docker)
task deploy:render TARGET=openshift  # manifests kustomize générés (aussi : openshift-external-db)
task migrate                         # migrations seules (compte DDL ; cf. database.migrate: false)
```

Les tests n'appellent jamais le réseau (httptest). Rechargement de config à chaud : `kill -HUP` ou `POST /api/admin/reload`.

## Principes non négociables

1. **Le YAML est la source de vérité de la configuration** ; la base ne stocke que état + historique.
   Une API retirée du YAML est désactivée (`enabled = 0`), **jamais supprimée** : on ne perd pas l'historique.
   L'`id` d'une API est une clé stable : ne jamais proposer de le renommer sans migration.
2. **On compare des formes normalisées, jamais des octets bruts.** Tout nouveau cas de faux positif se règle
   dans `normalize` (ou via `ignore:`), pas en filtrant après coup dans le diff.
3. **Erreur ≠ changement.** Un fetch en échec ne crée jamais de snapshot ni de `change`.
4. **Un snapshot n'est créé que si le hash normalisé change.** Les checks inchangés vont uniquement dans `checks`.
5. **Secrets** : uniquement via `${ENV_VAR}` ou `${file:/chemin}` dans le YAML ; jamais logués, jamais renvoyés
   par l'API REST, jamais stockés dans `apis.config_json`.
   **Les credentials ne partent que vers l'origine configurée** (schéma+hôte+port) : redirection cross-origin,
   spec découverte ou `$ref` externe sur un autre hôte → requête sans auth.
6. **Anti-SSRF** : tout fetch (y compris redirections et URLs découvertes) passe par `internal/fetch` et son contrôle
   `deny_cidrs`. Les sorties passent par le proxy d'entreprise (le DNS y est résolu) : le contrôle local est best effort,
   ne pas le présenter comme une garantie. Ne jamais ajouter de chemin réseau qui contourne `internal/fetch`.
7. **Notifications** : idempotentes (`UNIQUE(change_id, notifier)`), filtrées par `min_level` et `only_in_scope`.
   Le format de chaque plateforme vit dans son notifier ; le cœur ne manipule qu'un `ChangeEvent` neutre.
8. Pas d'infra supplémentaire (Redis, broker, worker séparé) sans ADR justifiant le besoin.
9. **Agnostique de la Gateway** : aucun connecteur spécifique à une plateforme (Kong, Apigee…). Le seul pont prévu est
   un contrat OpenAPI exporté (`gateway.published_spec`).
10. **Utilisable par n'importe quelle équipe** : toute nouvelle option de config est ajoutée au JSON Schema
    (`config/apis.schema.json`) et à `apis.example.yml` ; toute erreur remontée à l'utilisateur est en français et dit quoi faire.
11. **SQL portable** PostgreSQL/SQLite : placeholders `?` (réécrits en `$n`), `TRUE`/`FALSE`, `RETURNING` (jamais
    `LastInsertId`), `lower()` plutôt que `COLLATE NOCASE`. Toute modification du store passe `task test` **et** `task test:pg`.
12. **Sans état en `serve`** : aucune écriture disque (racine en lecture seule en production). Le scheduler ne tourne
    que sur le leader ; tout ce qui est planifié doit vivre dans `Scheduler.lead`.
13. Héritage de configuration : **API > groupe > defaults** (`applyDefaults`). Les profils réseau ne doivent jamais
    affaiblir le profil `default` ; l'accès au réseau interne est toujours un opt-in explicite (`network:`).
14. **Toute route HTTP est déclarée dans `Server.routes()` avec son scope** et décrite dans `httpapi/openapi.yaml`
    (le test `TestOpenAPICoversRoutes` échoue sinon). Aucune route ne modifie la configuration : elle reste as-code (ADR 0003).
15. Les fragments (`include:`) ne déclarent que `groups` et `apis` ; notifiers, profils réseau, base et serveur restent
    dans le fichier principal. Les messages de validation citent le fichier d'origine.
16. **Toute requête sortante dérivée d'un contenu partenaire** (scripts de page, candidats de découverte, `$ref`
    externes) passe par le `fetch.Client` de l'API, en http(s) uniquement, avec un nombre de requêtes borné. Ne jamais
    utiliser `openapi3.DefaultReadFromURI` ni lire un chemin local à partir d'une spec.
17. Ne pas changer la forme canonique des specs existantes (hash) sans migration : tout changement de normalisation
    produirait une « évolution » fantôme sur toutes les APIs. Le bundling n'est appliqué qu'aux specs ayant des `$ref` externes.

## Conventions

- Code, identifiants, commits : anglais. Docs, UI et messages d'erreur utilisateur : français.
  Exception : les libellés de changements viennent d'oasdiff, qui n'existe qu'en anglais — ne pas les traduire à la main.
- Go : `internal/` uniquement, interfaces définies côté consommateur, erreurs wrappées (`fmt.Errorf("...: %w", err)`),
  `context.Context` en premier paramètre de tout ce qui fait de l'I/O.
- Tests : table-driven ; fixtures de specs dans `backend/internal/*/testdata/` (paires `before`/`after` pour le diff,
  pages HTML Swagger-UI / Redoc réelles pour `discover`). Pas d'appel réseau réel dans les tests (`httptest.Server`).
- Front : composants fonctionnels, pas de state global hors TanStack Query, dates formatées via `Intl` dans la langue choisie.
- **i18n** : tout texte visible existe en `fr` et `en` (front : `src/i18n.tsx`, dont le type impose la parité des clés ;
  serveur : `internal/i18n`). Les erreurs exposées au front portent une catégorie (`error_kind`) que le front traduit.
- Toute modification de schéma = nouvelle migration `NNNN_description.sql`.
- Décision structurante = nouvel ADR dans `docs/adr/` (format court : contexte / décision / conséquences).
