// Package notify turns check outcomes into webhook notifications (Google Chat,
// Slack, Teams, generic JSON signed with HMAC), stores them and retries failures.
package notify

import (
	"fmt"
	"strings"
	"time"

	"git.k3nny.fr/kansabooj/internal/diff"
	"git.k3nny.fr/kansabooj/internal/i18n"
)

const (
	KindChange    = "change"
	KindStale     = "stale"
	KindRecovered = "recovered"

	maxListedItems = 8
)

// Event is the platform-neutral description of what happened. Notifiers only
// format it; they never decide whether to send.
type Event struct {
	Kind              string      `json:"kind"`
	APIID             string      `json:"api_id"`
	APIName           string      `json:"api_name"`
	Owner             string      `json:"owner,omitempty"`
	ChangeID          int64       `json:"change_id,omitempty"`
	OccurredAt        time.Time   `json:"occurred_at"`
	VersionFrom       string      `json:"version_from,omitempty"`
	VersionTo         string      `json:"version_to,omitempty"`
	MaxLevel          string      `json:"max_level,omitempty"`
	Items             []diff.Item `json:"items,omitempty"`      // items selected for this notifier
	OmittedOutOfScope int         `json:"omitted_out_of_scope"` // filtered out by only_in_scope
	Error             string      `json:"error,omitempty"`
	ConsecutiveErrors int         `json:"consecutive_errors,omitempty"`
	Link              string      `json:"link,omitempty"`
	counts            [3]int      // error, warn, info among Items
}

// message is the rendered, language-specific content shared by all formats.
type message struct {
	Title    string
	Subtitle string
	Lines    []string // plain text, one per item
	Footer   string
	Button   string
	Link     string
}

func render(e *Event, lang string) message {
	m := message{Link: e.Link}
	if e.Owner != "" {
		m.Footer = i18n.T(lang, "owner", e.Owner)
	}
	switch e.Kind {
	case KindStale:
		m.Title = i18n.T(lang, "stale.title", e.APIName)
		m.Subtitle = i18n.T(lang, "stale.body", e.ConsecutiveErrors, e.Error)
		m.Button = i18n.T(lang, "button.api")
	case KindRecovered:
		m.Title = i18n.T(lang, "recovered.title", e.APIName)
		m.Subtitle = i18n.T(lang, "recovered.body")
		m.Button = i18n.T(lang, "button.api")
	default:
		var parts []string
		for i, key := range []string{"change.breaking", "change.warn", "change.info"} {
			if e.counts[i] > 0 {
				parts = append(parts, i18n.T(lang, key, e.counts[i]))
			}
		}
		m.Title = i18n.T(lang, "change.title", e.APIName, strings.Join(parts, ", "))
		if e.VersionFrom != e.VersionTo {
			m.Subtitle = i18n.T(lang, "change.version", na(e.VersionFrom), na(e.VersionTo))
		} else {
			m.Subtitle = i18n.T(lang, "change.noversion", na(e.VersionTo))
		}
		for i, it := range e.Items {
			if i == maxListedItems {
				m.Lines = append(m.Lines, i18n.T(lang, "change.more", len(e.Items)-maxListedItems))
				break
			}
			op := strings.TrimSpace(it.Method + " " + it.Path)
			if op != "" {
				op += " — "
			}
			m.Lines = append(m.Lines, fmt.Sprintf("[%s] %s%s", i18n.T(lang, "level."+it.Level), op, it.Text))
		}
		if e.OmittedOutOfScope > 0 {
			m.Lines = append(m.Lines, i18n.T(lang, "change.outofscope", e.OmittedOutOfScope))
		}
		m.Button = i18n.T(lang, "button.view")
	}
	return m
}

func na(v string) string {
	if v == "" {
		return "n/a"
	}
	return v
}
