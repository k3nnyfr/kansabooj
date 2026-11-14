# ADR 0001 — Configuration en YAML (GitOps), état en base

**Statut** : proposé — 2026-11-14

## Contexte
La liste des APIs à surveiller pourrait vivre en base (avec UI admin) ou dans un fichier YAML versionné.

## Décision
`apis.yml` est la source de vérité, versionnée dans git et relue en MR. Il est synchronisé en base au démarrage et sur reload.
La base ne contient que l'état d'exécution et l'historique (snapshots, changes, notifications).

## Conséquences
- + Revue humaine de chaque URL ajoutée (limite le risque SSRF), traçabilité gratuite via git.
- + Pas d'UI admin ni d'authz fine à développer en v1 ; le front est read-only (hors acquittement).
- − Ajouter une API demande une MR ; acceptable pour une équipe API Gateway, moins pour des métiers.
- Si une UI admin devient nécessaire, elle ouvrira une MR sur le dépôt de config au lieu d'écrire en base.
