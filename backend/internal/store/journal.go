package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"
)

// JournalEntry is one action in the journal: a check (manual or scheduled), an acknowledgment
// or a notification. The journal is read from the checks, changes and notifications tables:
// nothing is stored twice, and it follows their retention (server.check_retention for checks).
type JournalEntry struct {
	Kind    string    `json:"kind"` // check | ack | notification
	At      time.Time `json:"at"`
	APIID   string    `json:"api_id"`
	APIName string    `json:"api_name"`
	Actor   string    `json:"actor,omitempty"`   // manual check, acknowledgment
	Trigger string    `json:"trigger,omitempty"` // check: manual | schedule | startup
	// check: unchanged | changed | error; ack: acknowledged;
	// notification: sent | failed | pending | queued | digested
	Status           string `json:"status"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
	ErrorKind        string `json:"error_kind,omitempty"`
	Error            string `json:"error,omitempty"`
	ChangeID         int64  `json:"change_id,omitempty"`
	Version          string `json:"version,omitempty"` // acknowledgment: version the change led to
	Notifier         string `json:"notifier,omitempty"`
	NotificationKind string `json:"notification_kind,omitempty"`
	Attempts         int    `json:"attempts,omitempty"`
}

// Kinds accepted by JournalQuery.Kinds.
const (
	JournalManualCheck    = "check_manual"
	JournalScheduledCheck = "check_scheduled"
	JournalAck            = "ack"
	JournalNotification   = "notification"
)

// JournalKinds lists every accepted kind, for validation.
var JournalKinds = []string{JournalManualCheck, JournalScheduledCheck, JournalAck, JournalNotification}

// JournalQuery selects a page of the journal, newest first.
type JournalQuery struct {
	Before time.Time // exclusive upper bound; zero = no bound
	Kinds  []string  // empty = every kind
	APIID  string    // empty = every API
	Limit  int
}

// Journal returns up to q.Limit entries older than q.Before, newest first, and the cursor of the
// next page (zero when there is none). A page never splits entries sharing a timestamp (an
// "acknowledge all" stamps every change with the same time): the next page starts with them.
func (s *Store) Journal(ctx context.Context, q JournalQuery) ([]JournalEntry, time.Time, error) {
	limit := min(max(q.Limit, 1), 200)
	want := func(k string) bool { return len(q.Kinds) == 0 || slices.Contains(q.Kinds, k) }
	before := q.Before
	if before.IsZero() {
		before = now().Add(time.Hour) // clocks of other replicas may run slightly ahead
	}
	filter := func(col string) (string, []any) {
		args := []any{before.UTC()}
		if q.APIID != "" {
			return " AND " + col + " = ?", append(args, q.APIID)
		}
		return "", args
	}

	var out []JournalEntry
	manual, scheduled := want(JournalManualCheck), want(JournalScheduledCheck)
	if manual || scheduled {
		cond, args := filter("c.api_id")
		switch {
		case manual && !scheduled:
			cond += " AND c.trigger = 'manual'"
		case scheduled && !manual:
			cond += " AND c.trigger <> 'manual'"
		}
		rows, err := s.db.QueryContext(ctx, `SELECT c.started_at, c.api_id, a.name, COALESCE(c.actor,''), c.trigger, c.status,
  COALESCE(c.duration_ms,0), COALESCE(c.error_kind,''), COALESCE(c.error,'')
FROM checks c JOIN apis a ON a.id = c.api_id
WHERE c.started_at < ?`+cond+` ORDER BY c.started_at DESC, c.id DESC LIMIT ?`, append(args, limit+1)...)
		if err != nil {
			return nil, time.Time{}, err
		}
		err = scanJournal(rows, &out, func(e *JournalEntry, at *sql.NullString) []any {
			e.Kind = "check"
			return []any{at, &e.APIID, &e.APIName, &e.Actor, &e.Trigger, &e.Status, &e.DurationMS, &e.ErrorKind, &e.Error}
		})
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	if want(JournalAck) {
		cond, args := filter("ch.api_id")
		rows, err := s.db.QueryContext(ctx, `SELECT ch.acknowledged_at, ch.api_id, a.name, COALESCE(ch.acknowledged_by,''), ch.id, COALESCE(ch.version_to,'')
FROM changes ch JOIN apis a ON a.id = ch.api_id
WHERE ch.acknowledged_at IS NOT NULL AND ch.acknowledged_at < ?`+cond+` ORDER BY ch.acknowledged_at DESC, ch.id DESC LIMIT ?`, append(args, limit+1)...)
		if err != nil {
			return nil, time.Time{}, err
		}
		err = scanJournal(rows, &out, func(e *JournalEntry, at *sql.NullString) []any {
			e.Kind, e.Status = "ack", "acknowledged"
			return []any{at, &e.APIID, &e.APIName, &e.Actor, &e.ChangeID, &e.Version}
		})
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	if want(JournalNotification) {
		cond, args := filter("n.api_id")
		rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(n.sent_at, n.created_at), n.api_id, a.name, n.notifier, n.kind, n.status, n.attempts,
  COALESCE(n.last_error,''), COALESCE(n.change_id,0)
FROM notifications n JOIN apis a ON a.id = n.api_id
WHERE COALESCE(n.sent_at, n.created_at) < ?`+cond+` ORDER BY COALESCE(n.sent_at, n.created_at) DESC, n.id DESC LIMIT ?`, append(args, limit+1)...)
		if err != nil {
			return nil, time.Time{}, err
		}
		err = scanJournal(rows, &out, func(e *JournalEntry, at *sql.NullString) []any {
			e.Kind = "notification"
			return []any{at, &e.APIID, &e.APIName, &e.Notifier, &e.NotificationKind, &e.Status, &e.Attempts, &e.Error, &e.ChangeID}
		})
		if err != nil {
			return nil, time.Time{}, err
		}
	}

	slices.SortStableFunc(out, func(a, b JournalEntry) int {
		if c := b.At.Compare(a.At); c != 0 {
			return c
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	if len(out) <= limit {
		return out, time.Time{}, nil
	}
	out = out[:limit]
	// Leave the entries sharing the oldest timestamp to the next page, unless the page holds nothing else.
	last, cut := out[len(out)-1].At, len(out)
	for cut > 0 && out[cut-1].At.Equal(last) {
		cut--
	}
	if cut == 0 {
		return out, last, nil // more than a page at one instant: some are skipped, rather than looping
	}
	// The cursor is exclusive: one microsecond after the tie (PostgreSQL keeps microseconds) brings it back.
	return out[:cut], last.Add(time.Microsecond), nil
}

func scanJournal(rows *sql.Rows, out *[]JournalEntry, dest func(*JournalEntry, *sql.NullString) []any) error {
	defer rows.Close()
	for rows.Next() {
		var e JournalEntry
		var at sql.NullString
		if err := rows.Scan(dest(&e, &at)...); err != nil {
			return err
		}
		if t := parseTime(at); t != nil {
			e.At = *t
		}
		*out = append(*out, e)
	}
	return rows.Err()
}
