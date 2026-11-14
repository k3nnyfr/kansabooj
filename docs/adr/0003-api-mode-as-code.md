# ADR 0003 — Mode API et gestion « as-code »

**Statut** : accepté et implémenté (v0.4) — 2026-11-14

## Contexte
On veut piloter Kansabooj sans passer par l'interface : déclarer des APIs depuis la CI d'une équipe productrice
(usage interne), intégrer l'état des contrats dans d'autres outils, automatiser les acquittements.

Deux lectures de « as-code » :
1. **la configuration est du code** (déclarative, versionnée, revue) ;
2. **une API d'écriture** que des outils appellent pour créer/modifier/supprimer des APIs surveillées.

## Décision
On fait (1) à fond et on n'ouvre pas (2) tant qu'un besoin concret ne l'impose pas.

- **Configuration as-code (déjà le cas, ADR 0001), rendue multi-équipes** : `config/apis.d/*.yml` fusionnés au chargement,
  un fichier par équipe/groupe, CODEOWNERS sur le dépôt de config. Chaque équipe ajoute ses APIs par MR, sans toucher aux autres.
- **API de lecture et d'actions, documentée** : Kansabooj publie sa propre spec OpenAPI (`GET /api/openapi.yaml`) — et peut
  donc se surveiller lui-même. Les actions utiles à l'automatisation existent : check, acquittement, reload.
- **Validation en CI** : `POST /api/config/validate` (et `kansabooj validate` hors ligne) pour valider une MR de config
  *avec le contexte du serveur* (variables d'environnement présentes, notifiers existants) avant merge.
- **Producteurs internes** : leur CI appelle `kansabooj diff kansabooj:<api-id> build/openapi.yaml` pour comparer la spec
  du build à la version enregistrée (périmètre et règles anti-bruit de l'API appliqués) et bloquer une rupture de contrat
  **avant** déploiement — sans rien écrire côté serveur.
- **Authentification des appels machine** : jetons d'API à portée limitée (`read`, `check`, `ack`, `validate`, `admin`),
  déclarés **dans la configuration sous forme de hash** (`server.tokens`, générés par `kansabooj token new`) : ils
  restent eux aussi « as-code » et révisables, le jeton lui-même n'étant connu que de l'appelant.

Implémentation : `include:` (fragments), `GET /api/openapi.yaml` (test garantissant que chaque route est décrite),
`POST /api/config/validate?fragment=…`, `server.tokens` / `require_identity` / `admins`, `kansabooj diff`, guide `docs/guides/ci.md`.

## Option écartée (pour l'instant) : CRUD des APIs par l'API
- Crée une **seconde source de vérité** (YAML vs base) avec des conflits à arbitrer.
- Réouvre le risque **SSRF** sans revue humaine des URLs (cf. §2.9 de l'architecture), d'autant plus avec les APIs internes.
- Si un besoin réel apparaît (ex. enregistrement automatique par une plateforme de déploiement) : « enregistrements dynamiques »
  stockés en base, marqués `origin=api`, **limités à un profil réseau dédié** et à des domaines autorisés, le YAML restant
  prioritaire en cas de conflit d'`id`. Un provider Terraform ne serait qu'un client de cette API.

## Conséquences
- + Pas de double source de vérité ; l'historique git reste l'audit de la configuration.
- + Les intégrations (tableaux de bord, ITSM, CI) disposent d'une API stable et documentée.
- − Ajouter une API reste une MR (quelques minutes) et non un appel HTTP instantané.
