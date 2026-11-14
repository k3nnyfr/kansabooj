-- Kansabooj — schéma initial (SQLite ; reste compatible Postgres à quelques types près)

-- Miroir de config/apis.yml. Jamais supprimé : une API retirée du YAML passe enabled = 0.
CREATE TABLE apis (
    id                  TEXT PRIMARY KEY,          -- slug du YAML
    name                TEXT NOT NULL,
    owner               TEXT,
    source_url          TEXT NOT NULL,
    source_type         TEXT NOT NULL,             -- openapi | swagger-ui | redoc | html | auto
    resolved_spec_url   TEXT,                      -- URL réelle trouvée par discover
    config_json         TEXT NOT NULL,             -- bloc YAML effectif (defaults fusionnés), secrets exclus
    enabled             INTEGER NOT NULL DEFAULT 1,
    status              TEXT NOT NULL DEFAULT 'pending', -- pending | ok | error | auth_error | stale
    last_error          TEXT,
    consecutive_errors  INTEGER NOT NULL DEFAULT 0,
    etag                TEXT,                      -- validateurs HTTP pour les requêtes conditionnelles
    last_modified       TEXT,
    current_snapshot_id INTEGER REFERENCES snapshots(id),
    last_checked_at     TIMESTAMP,
    next_check_at       TIMESTAMP,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Stockage content-addressed : une spec identique n'est stockée qu'une fois.
CREATE TABLE blobs (
    sha256      TEXT PRIMARY KEY,
    encoding    TEXT NOT NULL,                     -- gzip | zstd
    media_type  TEXT NOT NULL,                     -- application/json | application/yaml | text/html
    size        INTEGER NOT NULL,                  -- taille décompressée
    content     BLOB NOT NULL
);

-- Une ligne par version DISTINCTE (hash normalisé différent) d'une API.
CREATE TABLE snapshots (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    api_id           TEXT NOT NULL REFERENCES apis(id),
    fetched_at       TIMESTAMP NOT NULL,
    raw_sha256       TEXT NOT NULL REFERENCES blobs(sha256),
    normalized_sha256 TEXT NOT NULL REFERENCES blobs(sha256),
    spec_format      TEXT NOT NULL,                -- openapi-3.1 | openapi-3.0 | swagger-2.0 | html
    info_version     TEXT,
    title            TEXT,
    operation_count  INTEGER,
    UNIQUE (api_id, normalized_sha256)
);
CREATE INDEX idx_snapshots_api ON snapshots(api_id, fetched_at DESC);

-- Journal de chaque exécution (purgé après rétention configurable).
CREATE TABLE checks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    api_id       TEXT NOT NULL REFERENCES apis(id),
    started_at   TIMESTAMP NOT NULL,
    duration_ms  INTEGER,
    trigger      TEXT NOT NULL,                    -- schedule | manual | startup
    status       TEXT NOT NULL,                    -- unchanged | changed | error
    error_kind   TEXT,                             -- network | http | auth | denied | too_large | invalid_spec
    http_status  INTEGER,
    error        TEXT,
    snapshot_id  INTEGER REFERENCES snapshots(id)
);
CREATE INDEX idx_checks_api ON checks(api_id, started_at DESC);

-- Une évolution = passage d'un snapshot au suivant. C'est l'unité de la timeline.
CREATE TABLE changes (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    api_id            TEXT NOT NULL REFERENCES apis(id),
    from_snapshot_id  INTEGER REFERENCES snapshots(id),   -- NULL = première capture
    to_snapshot_id    INTEGER NOT NULL REFERENCES snapshots(id),
    detected_at       TIMESTAMP NOT NULL,
    version_from      TEXT,
    version_to        TEXT,
    error_count       INTEGER NOT NULL DEFAULT 0,  -- breaking
    warn_count        INTEGER NOT NULL DEFAULT 0,
    info_count        INTEGER NOT NULL DEFAULT 0,
    in_scope_count    INTEGER NOT NULL DEFAULT 0,  -- changements touchant le périmètre réexposé
    semantic          INTEGER NOT NULL DEFAULT 1,  -- 0 = mode dégradé (html), pas de détail
    acknowledged_at   TIMESTAMP,
    acknowledged_by   TEXT
);
CREATE INDEX idx_changes_api ON changes(api_id, detected_at DESC);

CREATE TABLE change_items (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    change_id  INTEGER NOT NULL REFERENCES changes(id) ON DELETE CASCADE,
    rule_id    TEXT NOT NULL,                      -- id de règle oasdiff (ex: response-property-removed)
    level      TEXT NOT NULL,                      -- error | warn | info
    method     TEXT,
    path       TEXT,
    in_scope   INTEGER NOT NULL DEFAULT 1,
    text       TEXT NOT NULL
);
CREATE INDEX idx_change_items_change ON change_items(change_id);

CREATE TABLE notifications (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    change_id    INTEGER REFERENCES changes(id),   -- NULL pour une notif d'erreur/stale
    api_id       TEXT NOT NULL REFERENCES apis(id),
    notifier     TEXT NOT NULL,                    -- nom du notifier dans le YAML
    kind         TEXT NOT NULL,                    -- change | stale | recovered
    status       TEXT NOT NULL DEFAULT 'pending',  -- pending | sent | failed | skipped
    attempts     INTEGER NOT NULL DEFAULT 0,
    last_error   TEXT,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at      TIMESTAMP,
    UNIQUE (change_id, notifier)
);
