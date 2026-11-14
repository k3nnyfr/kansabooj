# Kansabooj

Surveillance des contrats d'API partenaires réexposées dans l'API Gateway interne.

- Renseigner les sources dans `config/apis.yml` (voir `config/apis.example.yml`) : OpenAPI 3.x, Swagger 2.0, page Swagger-UI, Redoc, ou HTML.
- Kansabooj vérifie périodiquement chaque contrat, détecte les changements (version, opérations, paramètres, schémas),
  les classe (breaking / à surveiller / info), les historise et notifie Google Chat / Slack / Teams / webhook générique.
- Le front liste les APIs avec leur état et affiche la timeline des évolutions de chacune.

Statut : **v0.4** — serveur, interface web (FR/EN), notifications, groupes & tags, APIs internes, PostgreSQL (y compris
serveur tiers) / Kubernetes, configuration as-code multi-équipes, jetons d'API, contrôle de contrat en CI. Voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) et [docs/ROADMAP.md](docs/ROADMAP.md).

## Démarrage

Prérequis : Go 1.26, Node 22+, [Task](https://taskfile.dev).

```bash
task build                                          # binaire complet bin/kansabooj
./bin/kansabooj serve --config config/apis.demo.yml # http://localhost:8080
```

En développement : `task serve` et `task ui:dev` (http://localhost:5173) dans deux terminaux.
CLI : `task check -- --all`, `task history -- worldline-direct`, `task --list` pour le reste.

Pour ajouter une API : copier un bloc de `config/apis.example.yml` (autocomplétion IDE via `config/apis.schema.json`),
puis tester sans rien enregistrer : `task check CONFIG=config/apis.yml -- --dry-run mon-api`.

## Déploiement (Kubernetes / OpenShift)

1. Une base PostgreSQL ≥ 12 (ex. `deploy/kubernetes/postgres/cluster.yaml` avec CloudNativePG) exposant un Secret
   `kansabooj-db-app` avec une clé `uri`.
2. Un Secret `kansabooj-secrets` (webhooks, clés partenaires référencées en `${VAR}`) et, si besoin, `kansabooj-files`
   (certificats mTLS, secrets lus via `${file:/run/secrets/kansabooj/...}`).
3. Adapter `deploy/kubernetes/base/apis.yml` et l'image, puis `kubectl apply -k deploy/kubernetes/overlays/openshift`
   (ou `overlays/kubernetes`).

Base sur un serveur PostgreSQL tiers (équipe DBA, base managée) : section `database:` et overlay
`openshift-external-db`, voir [docs/guides/postgresql-externe.md](docs/guides/postgresql-externe.md).
Intégration CI (validation de config, contrôle de contrat) : [docs/guides/ci.md](docs/guides/ci.md).
API : `GET /api/openapi.yaml`.

La configuration est rechargée à chaud quand le ConfigMap change. Plusieurs réplicas peuvent tourner : une seule
planifie les checks. Essai local complet : `podman-compose -f deploy/compose.yaml up --build`.
