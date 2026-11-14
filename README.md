# Kansabooj

Surveillance des contrats d'API partenaires réexposées dans l'API Gateway interne.

- Renseigner les sources dans `config/apis.yml` (voir `config/apis.example.yml`) : OpenAPI 3.x, Swagger 2.0, page Swagger-UI, Redoc, ou HTML.
- Kansabooj vérifie périodiquement chaque contrat, détecte les changements (version, opérations, paramètres, schémas),
  les classe (breaking / à surveiller / info), les historise et notifie Google Chat / Slack / Teams / webhook générique.
- Le front liste les APIs avec leur état et affiche la timeline des évolutions de chacune.

Statut : **v0.1 (CLI)** — voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) et [docs/ROADMAP.md](docs/ROADMAP.md).

## Démarrage

Prérequis : Go 1.26.

```bash
make validate                     # valide config/apis.demo.yml
make check API=--all              # premier check des APIs de démo
make check API=worldline-direct   # second passage : 304 Not Modified
cd backend && go run ./cmd/kansabooj history --config ../config/apis.demo.yml --db ../data/kansabooj.db worldline-direct
```

Pour ajouter une API : copier un bloc de `config/apis.example.yml` (l'IDE propose l'autocomplétion via `config/apis.schema.json`),
puis tester sans rien enregistrer : `make check CONFIG=config/apis.yml API="--dry-run mon-api"`.
