package config

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Digest groups the change notifications of a notifier into one message per day or
// per week instead of one message per change.
type Digest struct {
	Every    string `yaml:"every" json:"every"`                 // daily | weekly
	At       string `yaml:"at" json:"at,omitempty"`             // HH:MM, local time of Timezone (default 08:00)
	Day      string `yaml:"day" json:"day,omitempty"`           // weekly: monday … sunday (default monday)
	Timezone string `yaml:"timezone" json:"timezone,omitempty"` // IANA name (default Europe/Paris)
	// ImmediateLevel: a change with at least one item at this level (or above) is still
	// sent right away, e.g. "error" for breaking changes. Empty: everything is digested.
	ImmediateLevel string `yaml:"immediate_level" json:"immediate_level,omitempty"`
}

var weekdays = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

func (d *Digest) applyDefaults() {
	if d.At == "" {
		d.At = "08:00"
	}
	if d.Every == "weekly" && d.Day == "" {
		d.Day = "monday"
	}
	if d.Timezone == "" {
		d.Timezone = "Europe/Paris"
	}
}

func (d *Digest) validate(where string, add func(string, ...any)) {
	if d.Every != "daily" && d.Every != "weekly" {
		add("%s.digest.every : %q inconnu (daily ou weekly)", where, d.Every)
	}
	if _, err := time.Parse("15:04", d.At); err != nil {
		add("%s.digest.at : %q n'est pas une heure HH:MM (ex. 08:30)", where, d.At)
	}
	if d.Every == "weekly" && !slices.Contains(weekdays, d.Day) {
		add("%s.digest.day : %q inconnu (%s)", where, d.Day, strings.Join(weekdays, ", "))
	}
	if _, err := time.LoadLocation(d.Timezone); err != nil {
		add("%s.digest.timezone : fuseau %q inconnu (nom IANA, ex. Europe/Paris)", where, d.Timezone)
	}
	if d.ImmediateLevel != "" && !slices.Contains(levels, d.ImmediateLevel) {
		add("%s.digest.immediate_level : %q inconnu (valeurs possibles : %s)", where, d.ImmediateLevel, strings.Join(levels, ", "))
	}
}

// LastDue returns the most recent scheduled digest time not after now.
func (d *Digest) LastDue(now time.Time) time.Time {
	loc, err := time.LoadLocation(d.Timezone)
	if err != nil {
		loc = time.UTC
	}
	at, err := time.Parse("15:04", d.At)
	if err != nil {
		at = time.Date(0, 1, 1, 8, 0, 0, 0, time.UTC)
	}
	local := now.In(loc)
	due := time.Date(local.Year(), local.Month(), local.Day(), at.Hour(), at.Minute(), 0, 0, loc)
	if due.After(local) {
		due = due.AddDate(0, 0, -1)
	}
	if d.Every == "weekly" {
		want := slices.Index(weekdays, d.Day)
		for int(due.Weekday()) != want {
			due = due.AddDate(0, 0, -1)
		}
	}
	return due
}

// Describe is a short human description ("tous les jours à 08:00").
func (d *Digest) Describe() string {
	if d.Every == "weekly" {
		return fmt.Sprintf("chaque %s à %s (%s)", d.Day, d.At, d.Timezone)
	}
	return fmt.Sprintf("tous les jours à %s (%s)", d.At, d.Timezone)
}
