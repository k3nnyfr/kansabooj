# Kansabooj

Surveillance des contrats d'API partenaires réexposées dans l'API Gateway interne.

- Renseigner les sources dans `config/apis.yml` (voir `config/apis.example.yml`) : OpenAPI 3.x, Swagger 2.0, page Swagger-UI, Redoc, ou HTML.
- Kansabooj vérifie périodiquement chaque contrat, détecte les changements (version, opérations, paramètres, schémas),
  les classe (breaking / à surveiller / info), les historise et notifie Google Chat / Slack / Teams / webhook générique.
- Le front liste les APIs avec leur état et affiche la timeline des évolutions de chacune.

Statut : **v0.2** — serveur, interface web (FR/EN), notifications. Voir [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) et [docs/ROADMAP.md](docs/ROADMAP.md).

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
