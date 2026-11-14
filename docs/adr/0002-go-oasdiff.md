# ADR 0002 — Backend en Go avec oasdiff en bibliothèque

**Statut** : proposé — 2026-11-14

## Contexte
Le cœur de valeur est le diff sémantique de contrats OpenAPI avec classification des changements cassants.
L'outil de référence, oasdiff, est écrit en Go et utilisable comme bibliothèque ; kin-openapi (Go) gère parsing,
conversion Swagger 2.0 → OAS 3 et résolution des `$ref`.

## Décision
Backend en Go, oasdiff et kin-openapi importés comme bibliothèques. Front React/TS embarqué dans le binaire.

## Conséquences
- + Accès typé aux changements (règle, niveau, opération), pas de parsing de sortie CLI.
- + Un binaire unique, image distroless, déploiement trivial.
- − Si l'équipe n'a pas de compétence Go, l'alternative est Python/FastAPI + binaire oasdiff appelé en sous-processus
  (sortie JSON) — viable, mais couplage à un format de sortie CLI et une dépendance binaire de plus.
