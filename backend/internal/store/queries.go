package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("introuvable")

// APIRow is an API as listed in the UI.
type APIRow struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Owner             string     `json:"owner,omitempty"`
	Group             string     `json:"group,omitempty"`
	Tags              []string   `json:"tags"`
	Network           string     `json:"network"`
	SourceURL         string     `json:"source_url"`
	SourceType        string     `json:"source_type"`
	Enabled           bool       `json:"enabled"`
	Status            string     `json:"status"`
	LastError         string     `json:"last_error,omitempty"`
	LastErrorKind     string     `json:"last_error_kind,omitempty"`
	ConsecutiveErrors int        `json:"consecutive_errors"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	NextCheckAt       *time.Time `json:"next_check_at,omitempty"`
	Version           string     `json:"version,omitempty"`
	Format            string     `json:"format,omitempty"`
	Title             string     `json:"title,omitempty"`
	OperationCount    int        `json:"operation_count"`
	SnapshotID        *int64     `json:"snapshot_id,omitempty"`
	LastChangeAt      *time.Time `json:"last_change_at,omitempty"`
	ChangeCount       int        `json:"change_count"`
	Unacknowledged    int        `json:"unacknowledged"`          // changes not yet seen (first capture excluded)
	UnackedBreaking   int        `json:"unacknowledged_breaking"` // of which with in-scope breaking items
	ConfigJSON        string     `json:"-"`
}

const apiSelect = `
SELECT a.id, a.name, COALESCE(a.owner,''), COALESCE(a.group_id,''), a.tags, a.network_profile, a.source_url, a.source_type, a.enabled, a.status, COALESCE(a.last_error,''),
  COALESCE((SELECT c.error_kind FROM checks c WHERE c.api_id = a.id ORDER BY c.started_at DESC, c.id DESC LIMIT 1), ''),
  a.consecutive_errors, a.last_checked_at, a.next_check_at,
  COALESCE(s.info_version,''), COALESCE(s.spec_format,''), COALESCE(s.title,''), COALESCE(s.operation_count,0), a.current_snapshot_id,
  (SELECT MAX(detected_at) FROM changes ch WHERE ch.api_id = a.id AND ch.from_snapshot_id IS NOT NULL),
  (SELECT COUNT(*) FROM changes ch WHERE ch.api_id = a.id),
  (SELECT COUNT(*) FROM changes ch WHERE ch.api_id = a.id AND ch.acknowledged_at IS NULL AND ch.from_snapshot_id IS NOT NULL),
  (SELECT COUNT(*) FROM changes ch WHERE ch.api_id = a.id AND ch.acknowledged_at IS NULL AND ch.from_snapshot_id IS NOT NULL
     AND EXISTS (SELECT 1 FROM change_items i WHERE i.change_id = ch.id AND i.level = 'error' AND i.in_scope = TRUE)),
  a.config_json
FROM apis a LEFT JOIN snapshots s ON s.id = a.current_snapshot_id`

func scanAPI(sc interface{ Scan(...any) error }) (*APIRow, error) {
	var r APIRow
	var last, next, lastChange sql.NullString
	var snap sql.NullInt64
	var tags string
	err := sc.Scan(&r.ID, &r.Name, &r.Owner, &r.Group, &tags, &r.Network, &r.SourceURL, &r.SourceType, &r.Enabled, &r.Status, &r.LastError, &r.LastErrorKind,
		&r.ConsecutiveErrors, &last, &next, &r.Version, &r.Format, &r.Title, &r.OperationCount, &snap,
		&lastChange, &r.ChangeCount, &r.Unacknowledged, &r.UnackedBreaking, &r.ConfigJSON)
	if err != nil {
		return nil, err
	}
	r.LastCheckedAt, r.NextCheckAt, r.LastChangeAt = parseTime(last), parseTime(next), parseTime(lastChange)
	r.Tags = []string{}
	json.Unmarshal([]byte(tags), &r.Tags)
	if snap.Valid {
		r.SnapshotID = &snap.Int64
	}
	return &r, nil
}

// ListAPIs returns enabled APIs, or all of them with includeDisabled.
func (s *Store) ListAPIs(ctx context.Context, includeDisabled bool) ([]APIRow, error) {
	q := apiSelect
	if !includeDisabled {
		q += ` WHERE a.enabled = TRUE`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY lower(a.name), a.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIRow{}
	for rows.Next() {
		r, err := scanAPI(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetAPI(ctx context.Context, id string) (*APIRow, error) {
	r, err := scanAPI(s.db.QueryRowContext(ctx, apiSelect+` WHERE a.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// TimelineEntry is a change as shown in the timeline.
type TimelineEntry struct {
	ID             int64      `json:"id"`
	DetectedAt     time.Time  `json:"detected_at"`
	FromSnapshotID *int64     `json:"from_snapshot_id,omitempty"`
	ToSnapshotID   int64      `json:"to_snapshot_id"`
	VersionFrom    string     `json:"version_from,omitempty"`
	VersionTo      string     `json:"version_to,omitempty"`
	Errors         int        `json:"errors"`
	Warns          int        `json:"warns"`
	Infos          int        `json:"infos"`
	InScope        int        `json:"in_scope"`
	InScopeErrors  int        `json:"in_scope_errors"`
	FirstCapture   bool       `json:"first_capture"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
}

const changeSelect = `
SELECT ch.id, ch.detected_at, ch.from_snapshot_id, ch.to_snapshot_id, COALESCE(ch.version_from,''), COALESCE(ch.version_to,''),
  ch.error_count, ch.warn_count, ch.info_count, ch.in_scope_count,
  (SELECT COUNT(*) FROM change_items i WHERE i.change_id = ch.id AND i.level = 'error' AND i.in_scope = TRUE),
  ch.acknowledged_at, COALESCE(ch.acknowledged_by,'')
FROM changes ch`

func scanChange(sc interface{ Scan(...any) error }) (*TimelineEntry, error) {
	var e TimelineEntry
	var detected string
	var from sql.NullInt64
	var ack sql.NullString
	if err := sc.Scan(&e.ID, &detected, &from, &e.ToSnapshotID, &e.VersionFrom, &e.VersionTo,
		&e.Errors, &e.Warns, &e.Infos, &e.InScope, &e.InScopeErrors, &ack, &e.AcknowledgedBy); err != nil {
		return nil, err
	}
	if t := parseTime(sql.NullString{String: detected, Valid: true}); t != nil {
		e.DetectedAt = *t
	}
	if from.Valid {
		e.FromSnapshotID = &from.Int64
	} else {
		e.FirstCapture = true
	}
	e.AcknowledgedAt = parseTime(ack)
	return &e, nil
}

// TimelinePage returns changes of an API, most recent first; before (change id) paginates.
func (s *Store) TimelinePage(ctx context.Context, apiID string, limit int, before int64) ([]TimelineEntry, error) {
	q, args := changeSelect+` WHERE ch.api_id = ?`, []any{apiID}
	if before > 0 {
		q += ` AND ch.id < ?`
		args = append(args, before)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY ch.id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TimelineEntry{}
	for rows.Next() {
		e, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ChangeItemRow is a stored unit change.
type ChangeItemRow struct {
	RuleID  string `json:"rule_id"`
	Level   string `json:"level"`
	Method  string `json:"method,omitempty"`
	Path    string `json:"path,omitempty"`
	InScope bool   `json:"in_scope"`
	Text    string `json:"text"`
}

type ChangeDetail struct {
	TimelineEntry
	APIID string          `json:"api_id"`
	Items []ChangeItemRow `json:"items"`
}

func (s *Store) GetChange(ctx context.Context, id int64) (*ChangeDetail, error) {
	row := s.db.QueryRowContext(ctx, `SELECT api_id FROM changes WHERE id = ?`, id)
	var d ChangeDetail
	if err := row.Scan(&d.APIID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	e, err := scanChange(s.db.QueryRowContext(ctx, changeSelect+` WHERE ch.id = ?`, id))
	if err != nil {
		return nil, err
	}
	d.TimelineEntry = *e
	rows, err := s.db.QueryContext(ctx, `SELECT rule_id, level, COALESCE(method,''), COALESCE(path,''), in_scope, text
FROM change_items WHERE change_id = ? ORDER BY CASE level WHEN 'error' THEN 0 WHEN 'warn' THEN 1 ELSE 2 END, path, method, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d.Items = []ChangeItemRow{}
	for rows.Next() {
		var it ChangeItemRow
		if err := rows.Scan(&it.RuleID, &it.Level, &it.Method, &it.Path, &it.InScope, &it.Text); err != nil {
			return nil, err
		}
		d.Items = append(d.Items, it)
	}
	return &d, rows.Err()
}

// Acknowledge marks a change as seen. Already acknowledged changes keep their first acknowledgement.
func (s *Store) Acknowledge(ctx context.Context, changeID int64, user string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE changes SET acknowledged_at = COALESCE(acknowledged_at, ?), acknowledged_by = COALESCE(acknowledged_by, ?) WHERE id = ?`, now(), user, changeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AcknowledgeAll marks every pending change of an API as seen and returns how many were updated.
func (s *Store) AcknowledgeAll(ctx context.Context, apiID, user string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE changes SET acknowledged_at = ?, acknowledged_by = ? WHERE api_id = ? AND acknowledged_at IS NULL`, now(), user, apiID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SnapshotSpec returns the raw or normalized content of a snapshot.
func (s *Store) SnapshotSpec(ctx context.Context, snapshotID int64, normalized bool) (apiID string, content []byte, mediaType string, err error) {
	col := "raw_sha256"
	if normalized {
		col = "normalized_sha256"
	}
	var sha string
	err = s.db.QueryRowContext(ctx, `SELECT s.api_id, s.`+col+`, b.media_type FROM snapshots s JOIN blobs b ON b.sha256 = s.`+col+` WHERE s.id = ?`, snapshotID).
		Scan(&apiID, &sha, &mediaType)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, "", ErrNotFound
	}
	if err != nil {
		return "", nil, "", err
	}
	content, err = s.Blob(ctx, sha)
	return apiID, content, mediaType, err
}

// CheckRow is a check log entry.
type CheckRow struct {
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Trigger    string    `json:"trigger"`
	Status     string    `json:"status"`
	ErrorKind  string    `json:"error_kind,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
	Error      string    `json:"error,omitempty"`
}

func (s *Store) RecentChecks(ctx context.Context, apiID string, limit int) ([]CheckRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT started_at, COALESCE(duration_ms,0), trigger, status, COALESCE(error_kind,''), COALESCE(http_status,0), COALESCE(error,'')
FROM checks WHERE api_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, apiID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CheckRow{}
	for rows.Next() {
		var c CheckRow
		var started string
		if err := rows.Scan(&started, &c.DurationMS, &c.Trigger, &c.Status, &c.ErrorKind, &c.HTTPStatus, &c.Error); err != nil {
			return nil, err
		}
		if t := parseTime(sql.NullString{String: started, Valid: true}); t != nil {
			c.StartedAt = *t
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NextChecks returns the persisted schedule.
func (s *Store) NextChecks(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, next_check_at FROM apis WHERE enabled = TRUE AND next_check_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var next sql.NullString
		if err := rows.Scan(&id, &next); err != nil {
			return nil, err
		}
		if t := parseTime(next); t != nil {
			out[id] = *t
		}
	}
	return out, rows.Err()
}

func (s *Store) SetNextCheck(ctx context.Context, apiID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE apis SET next_check_at = ? WHERE id = ?`, at.UTC(), apiID)
	return err
}

// PurgeChecks deletes check logs older than before, keeping those referenced by a snapshot.
func (s *Store) PurgeChecks(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE started_at < ? AND snapshot_id IS NULL`, before.UTC())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Notification is an outgoing webhook call.
type Notification struct {
	ID          int64
	ChangeID    *int64
	APIID       string
	Notifier    string
	Kind        string // change | stale | recovered
	Payload     []byte
	ContentType string
	Attempts    int
}

// CreateNotification stores a pending notification. A change is notified at most
// once per notifier: a duplicate returns created=false.
func (s *Store) CreateNotification(ctx context.Context, n *Notification) (created bool, err error) {
	err = s.db.QueryRowContext(ctx, `INSERT INTO notifications (change_id, api_id, notifier, kind, status, payload, content_type, next_attempt_at)
VALUES (?, ?, ?, ?, 'pending', ?, ?, ?) ON CONFLICT DO NOTHING RETURNING id`, n.ChangeID, n.APIID, n.Notifier, n.Kind, string(n.Payload), n.ContentType, now()).Scan(&n.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// MarkNotification records the result of a delivery attempt. next == nil with
// err != "" means the notification is abandoned.
func (s *Store) MarkNotification(ctx context.Context, id int64, sendErr string, next *time.Time) error {
	switch {
	case sendErr == "":
		_, err := s.db.ExecContext(ctx, `UPDATE notifications SET status = 'sent', attempts = attempts + 1, sent_at = ?, last_error = NULL, next_attempt_at = NULL WHERE id = ?`, now(), id)
		return err
	case next != nil:
		_, err := s.db.ExecContext(ctx, `UPDATE notifications SET attempts = attempts + 1, last_error = ?, next_attempt_at = ? WHERE id = ?`, sendErr, next.UTC(), id)
		return err
	default:
		_, err := s.db.ExecContext(ctx, `UPDATE notifications SET status = 'failed', attempts = attempts + 1, last_error = ?, next_attempt_at = NULL WHERE id = ?`, sendErr, id)
		return err
	}
}

// DueNotifications returns pending notifications whose next attempt is due.
func (s *Store) DueNotifications(ctx context.Context, at time.Time) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, change_id, api_id, notifier, kind, COALESCE(payload,''), COALESCE(content_type,''), attempts
FROM notifications WHERE status = 'pending' AND next_attempt_at <= ? ORDER BY id LIMIT 100`, at.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var change sql.NullInt64
		var payload string
		if err := rows.Scan(&n.ID, &change, &n.APIID, &n.Notifier, &n.Kind, &payload, &n.ContentType, &n.Attempts); err != nil {
			return nil, err
		}
		if change.Valid {
			n.ChangeID = &change.Int64
		}
		n.Payload = []byte(payload)
		out = append(out, n)
	}
	return out, rows.Err()
}

// parseTime reads timestamps as stored by modernc/sqlite (time.Time.String format) or RFC 3339.
func parseTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, v.String); err == nil {
			return &t
		}
	}
	return nil
}
