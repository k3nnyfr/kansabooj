// Package metrics exposes Prometheus metrics. Process counters (checks, notifications,
// reloads) are per replica; per-API gauges are read from the database at scrape time,
// so every replica reports the same, consistent values.
package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"git.k3nny.fr/kansabooj/internal/store"
)

var (
	checks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kansabooj_checks_total", Help: "Checks exécutés par cette instance, par API et résultat.",
	}, []string{"api", "status"})
	checkDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "kansabooj_check_duration_seconds", Help: "Durée des checks.",
		Buckets: []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
	}, []string{"api"})
	notifications = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kansabooj_notifications_total", Help: "Tentatives d'envoi de notifications, par notifier et résultat.",
	}, []string{"notifier", "kind", "result"})
	reloads = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kansabooj_config_reloads_total", Help: "Rechargements de configuration, par résultat.",
	}, []string{"result"})
	leader = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kansabooj_scheduler_leader", Help: "1 si cette instance planifie les checks.",
	})
	buildInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kansabooj_build_info", Help: "Version de Kansabooj.",
	}, []string{"version"})
)

// ObserveCheck records a finished check.
func ObserveCheck(api, status string, d time.Duration) {
	checks.WithLabelValues(api, status).Inc()
	checkDuration.WithLabelValues(api).Observe(d.Seconds())
}

// ObserveNotification records a delivery attempt (result: sent | retry | failed).
func ObserveNotification(notifier, kind, result string) {
	notifications.WithLabelValues(notifier, kind, result).Inc()
}

// ObserveReload records a configuration reload (result: ok | rejected).
func ObserveReload(ok bool) {
	if ok {
		reloads.WithLabelValues("ok").Inc()
	} else {
		reloads.WithLabelValues("rejected").Inc()
	}
}

// SetLeader reports whether this instance is the scheduler leader.
func SetLeader(v bool) {
	if v {
		leader.Set(1)
	} else {
		leader.Set(0)
	}
}

// Handler serves the metrics; version feeds kansabooj_build_info.
func Handler(st *store.Store, version string) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		checks, checkDuration, notifications, reloads, leader, buildInfo, &apiCollector{store: st})
	buildInfo.WithLabelValues(version).Set(1)
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}

var statuses = []string{"pending", "ok", "error", "auth_error", "stale"}

var (
	descStatus = prometheus.NewDesc("kansabooj_api_status", "État de l'API (1 pour l'état courant).", []string{"api", "group", "status"}, nil)
	descUnack  = prometheus.NewDesc("kansabooj_api_unacknowledged_changes", "Évolutions non vues.", []string{"api", "group"}, nil)
	descBreak  = prometheus.NewDesc("kansabooj_api_unacknowledged_breaking_changes", "Évolutions non vues avec changement cassant dans le périmètre.", []string{"api", "group"}, nil)
	descErrors = prometheus.NewDesc("kansabooj_api_consecutive_errors", "Échecs consécutifs.", []string{"api", "group"}, nil)
	descLast   = prometheus.NewDesc("kansabooj_api_last_check_timestamp_seconds", "Date du dernier check (epoch).", []string{"api", "group"}, nil)
	descUp     = prometheus.NewDesc("kansabooj_database_up", "1 si la base répond au moment de la collecte.", nil, nil)
)

// apiCollector reads per-API state from the database at scrape time.
type apiCollector struct{ store *store.Store }

func (c *apiCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descStatus, descUnack, descBreak, descErrors, descLast, descUp} {
		ch <- d
	}
}

func (c *apiCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	apis, err := c.store.ListAPIs(ctx, false)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(descUp, prometheus.GaugeValue, 0)
		return
	}
	ch <- prometheus.MustNewConstMetric(descUp, prometheus.GaugeValue, 1)
	for _, a := range apis {
		for _, s := range statuses {
			v := 0.0
			if a.Status == s {
				v = 1
			}
			ch <- prometheus.MustNewConstMetric(descStatus, prometheus.GaugeValue, v, a.ID, a.Group, s)
		}
		ch <- prometheus.MustNewConstMetric(descUnack, prometheus.GaugeValue, float64(a.Unacknowledged), a.ID, a.Group)
		ch <- prometheus.MustNewConstMetric(descBreak, prometheus.GaugeValue, float64(a.UnackedBreaking), a.ID, a.Group)
		ch <- prometheus.MustNewConstMetric(descErrors, prometheus.GaugeValue, float64(a.ConsecutiveErrors), a.ID, a.Group)
		if a.LastCheckedAt != nil {
			ch <- prometheus.MustNewConstMetric(descLast, prometheus.GaugeValue, float64(a.LastCheckedAt.Unix()), a.ID, a.Group)
		}
	}
}
