-- v0.6 : récapitulatifs de notifications. Les changements destinés à un notifier en mode digest sont stockés
-- avec status = 'queued' (payload = événement neutre) jusqu'au prochain récapitulatif, puis passent à 'digested'.
CREATE TABLE digest_runs (
    notifier    TEXT PRIMARY KEY,
    last_run_at TIMESTAMP NOT NULL   -- échéance planifiée du dernier récapitulatif traité
);
CREATE INDEX idx_notifications_queued ON notifications(notifier, status);
