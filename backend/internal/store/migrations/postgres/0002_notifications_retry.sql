ALTER TABLE notifications ADD COLUMN payload TEXT;
ALTER TABLE notifications ADD COLUMN content_type TEXT;
ALTER TABLE notifications ADD COLUMN next_attempt_at TIMESTAMPTZ;
CREATE INDEX idx_notifications_pending ON notifications(status, next_attempt_at);
CREATE INDEX idx_changes_unacked ON changes(api_id) WHERE acknowledged_at IS NULL;
