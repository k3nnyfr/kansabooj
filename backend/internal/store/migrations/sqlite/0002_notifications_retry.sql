-- v0.2 : notifications rejouables (payload figé à la création) et planification persistée
ALTER TABLE notifications ADD COLUMN payload TEXT;           -- corps JSON envoyé au webhook
ALTER TABLE notifications ADD COLUMN content_type TEXT;
ALTER TABLE notifications ADD COLUMN next_attempt_at TIMESTAMP;
CREATE INDEX idx_notifications_pending ON notifications(status, next_attempt_at);
CREATE INDEX idx_changes_unacked ON changes(api_id) WHERE acknowledged_at IS NULL;
