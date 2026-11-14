// Package scheduler runs periodic checks, owns the live configuration (reload)
// and wires check outcomes to notifications.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"os"
	"sort"
	"sync"
	"time"

	"git.k3nny.fr/kansabooj/internal/config"
	"git.k3nny.fr/kansabooj/internal/metrics"
	"git.k3nny.fr/kansabooj/internal/monitor"
	"git.k3nny.fr/kansabooj/internal/notify"
	"git.k3nny.fr/kansabooj/internal/store"
)

// ErrBusy is returned when a check of the same API is already running.
var ErrBusy = errors.New("un check de cette API est déjà en cours")

// ErrUnknownAPI is returned for an id absent from the configuration.
var ErrUnknownAPI = errors.New("API absente de la configuration")

type Scheduler struct {
	path  string
	store *store.Store
	log   *slog.Logger
	disp  *notify.Dispatcher
	sem   chan struct{}
	tick  time.Duration
	retry time.Duration // leader election retry

	mu        sync.Mutex
	cfg       *config.Config
	mon       *monitor.Monitor
	next      map[string]time.Time
	running   map[string]bool
	hostLocks map[string]*sync.Mutex
	lastTick  time.Time
	loadedAt  time.Time
	leader    bool
	instance  string
}

const (
	leaderLock     = "scheduler"
	leaderRetry    = 15 * time.Second
	leaderHeartbit = 10 * time.Second
)

func New(path string, cfg *config.Config, st *store.Store, log *slog.Logger) (*Scheduler, error) {
	s := &Scheduler{path: path, store: st, log: log, cfg: cfg, mon: monitor.New(cfg, st),
		sem: make(chan struct{}, cfg.Server.Concurrency), tick: 10 * time.Second, retry: leaderRetry,
		next: map[string]time.Time{}, running: map[string]bool{}, hostLocks: map[string]*sync.Mutex{}, loadedAt: time.Now()}
	s.instance, _ = os.Hostname()
	d, err := notify.NewDispatcher(st, s.Config, log)
	if err != nil {
		return nil, err
	}
	s.disp = d
	return s, nil
}

// ConfigPath returns the path of the main configuration file.
func (s *Scheduler) ConfigPath() string { return s.path }

// Config returns the live configuration.
func (s *Scheduler) Config() *config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Run schedules checks until ctx is cancelled. With several replicas (PostgreSQL),
// only the instance holding the leader lock schedules checks, retries notifications
// and purges logs; the others serve the UI/API and run manual checks.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		lock, ok, err := s.store.TryLock(ctx, leaderLock)
		if err != nil {
			s.log.Error("élection du leader", "err", err)
		}
		if ok {
			s.setLeader(true)
			s.log.Info("instance leader : planification active", "instance", s.instance)
			err = s.lead(ctx, lock)
			lock.Release()
			s.setLeader(false)
			if ctx.Err() != nil {
				return nil
			}
			s.log.Warn("leadership perdue", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(s.retry):
		}
	}
}

func (s *Scheduler) setLeader(v bool) {
	s.mu.Lock()
	s.leader = v
	s.mu.Unlock()
	metrics.SetLeader(v)
}

// lead runs the scheduling loop while the leader lock is held.
func (s *Scheduler) lead(parent context.Context, lock *store.Lock) error {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	go func() { // lose leadership as soon as the session holding the lock is gone
		t := time.NewTicker(leaderHeartbit)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := lock.Alive(ctx); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()

	persisted, err := s.store.NextChecks(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	now := time.Now()
	s.next = map[string]time.Time{}
	for _, a := range s.cfg.APIs {
		if t, ok := persisted[a.ID]; ok && t.After(now) {
			s.next[a.ID] = t
		} else {
			s.next[a.ID] = now.Add(s.jitter()) // spread the startup burst
		}
	}
	s.mu.Unlock()

	go s.disp.RetryLoop(ctx, 30*time.Second)
	go s.disp.DigestLoop(ctx, time.Minute)
	go s.purgeLoop(ctx)

	t := time.NewTicker(s.tick)
	defer t.Stop()
	var wg sync.WaitGroup
	for {
		s.dispatchDue(ctx, &wg)
		select {
		case <-ctx.Done():
			wg.Wait()
			return context.Cause(ctx)
		case <-t.C:
		}
	}
}

func (s *Scheduler) dispatchDue(ctx context.Context, wg *sync.WaitGroup) {
	s.mu.Lock()
	now := time.Now()
	s.lastTick = now
	var due []string
	for id, at := range s.next {
		if !at.After(now) && !s.running[id] {
			due = append(due, id)
			s.running[id] = true
		}
	}
	s.mu.Unlock()
	sort.Strings(due)
	for _, id := range due {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.run(ctx, id, "schedule")
		}()
	}
}

// TriggerCheck runs a check now, synchronously.
func (s *Scheduler) TriggerCheck(ctx context.Context, apiID string) (*monitor.Outcome, error) {
	s.mu.Lock()
	if _, ok := s.cfg.API(apiID); !ok {
		s.mu.Unlock()
		return nil, ErrUnknownAPI
	}
	if s.running[apiID] {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	s.running[apiID] = true
	s.mu.Unlock()
	return s.run(ctx, apiID, "manual")
}

// run executes one check; the caller has set running[apiID].
func (s *Scheduler) run(ctx context.Context, apiID, trigger string) (*monitor.Outcome, error) {
	defer func() {
		s.mu.Lock()
		delete(s.running, apiID)
		s.mu.Unlock()
	}()

	s.mu.Lock()
	cfg, mon := s.cfg, s.mon
	api, ok := cfg.API(apiID)
	s.mu.Unlock()
	if !ok {
		return nil, ErrUnknownAPI // removed by a reload in the meantime
	}

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	hl := s.hostLock(api.Source.URL) // one fetch at a time per partner host
	hl.Lock()
	defer hl.Unlock()

	// Across replicas: never two checks of the same API at once.
	lock, ok, err := s.store.TryLock(ctx, "api:"+apiID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrBusy
	}
	defer lock.Release()

	o, err := mon.Check(ctx, apiID, trigger, false)
	if errors.Is(err, store.ErrConcurrentUpdate) {
		return nil, ErrBusy
	}

	s.mu.Lock()
	next := time.Now().Add(api.Interval.Std() + s.jitter())
	if _, still := s.cfg.API(apiID); still {
		s.next[apiID] = next
	}
	s.mu.Unlock()
	if perr := s.store.SetNextCheck(context.WithoutCancel(ctx), apiID, next); perr != nil {
		s.log.Error("persistance du prochain check", "api", apiID, "err", perr)
	}

	if err != nil {
		s.log.Error("check en échec interne", "api", apiID, "err", err)
		return nil, err
	}
	metrics.ObserveCheck(apiID, o.Status, o.Duration)
	attrs := []any{"api", apiID, "trigger", trigger, "status", o.Status, "duration", o.Duration.Round(time.Millisecond)}
	switch o.Status {
	case monitor.StatusError:
		s.log.Warn("check", append(attrs, "error_kind", o.ErrorKind, "err", o.Err, "consecutive", o.ConsecutiveErrors)...)
	case monitor.StatusChanged:
		s.log.Info("check", append(attrs, "change_id", o.ChangeID, "breaking", o.Summary.Errors, "in_scope", o.Summary.InScope)...)
	default:
		s.log.Debug("check", attrs...)
	}
	s.disp.HandleOutcome(context.WithoutCancel(ctx), api, o)
	return o, nil
}

// Reload re-reads the configuration file. On error the running configuration is kept.
func (s *Scheduler) Reload(ctx context.Context) error {
	cfg, err := config.Load(s.path)
	if err == nil {
		err = s.store.SyncAPIs(ctx, cfg.APIs)
	}
	metrics.ObserveReload(err == nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg, s.mon, s.loadedAt = cfg, monitor.New(cfg, s.store), time.Now()
	keep := map[string]bool{}
	for _, a := range cfg.APIs {
		keep[a.ID] = true
		if _, ok := s.next[a.ID]; !ok {
			s.next[a.ID] = time.Now().Add(s.jitter())
		}
	}
	for id := range s.next {
		if !keep[id] {
			delete(s.next, id)
		}
	}
	s.log.Info("configuration rechargée", "apis", len(cfg.APIs))
	return nil
}

// WatchConfig reloads the configuration when the YAML file or a referenced secret
// file changes (Kubernetes updates mounted ConfigMaps/Secrets in place). Polling is
// used on purpose: it survives the symlink swaps done by the kubelet.
func (s *Scheduler) WatchConfig(ctx context.Context, every time.Duration) {
	last := s.Config().Fingerprint()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fp := s.Config().Fingerprint()
		if fp == last {
			continue
		}
		s.log.Info("modification de configuration détectée, rechargement")
		if err := s.Reload(ctx); err != nil {
			s.log.Error("rechargement refusé, configuration précédente conservée", "err", err)
		}
		last = fp // do not retry an invalid config every tick; the next edit triggers a new attempt
	}
}

// Status describes the scheduler for the status page.
type Status struct {
	Instance   string               `json:"instance"`
	Leader     bool                 `json:"leader"`
	LastTick   time.Time            `json:"last_tick"`
	ConfigAt   time.Time            `json:"config_loaded_at"`
	Running    []string             `json:"running"`
	NextChecks map[string]time.Time `json:"next_checks"`
}

func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Instance: s.instance, Leader: s.leader, LastTick: s.lastTick, ConfigAt: s.loadedAt, Running: []string{}, NextChecks: map[string]time.Time{}}
	for id := range s.running {
		st.Running = append(st.Running, id)
	}
	sort.Strings(st.Running)
	for id, t := range s.next {
		st.NextChecks[id] = t
	}
	return st
}

func (s *Scheduler) purgeLoop(ctx context.Context) {
	for {
		before := time.Now().Add(-s.Config().Server.CheckRetention.Std())
		if n, err := s.store.PurgeChecks(ctx, before); err != nil {
			s.log.Error("purge des checks", "err", err)
		} else if n > 0 {
			s.log.Info("purge des checks", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

func (s *Scheduler) hostLock(rawURL string) *sync.Mutex {
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		host = u.Host
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.hostLocks[host]
	if !ok {
		l = &sync.Mutex{}
		s.hostLocks[host] = l
	}
	return l
}

// jitter must be called with s.mu held or before concurrent use.
func (s *Scheduler) jitter() time.Duration {
	j := s.cfg.Defaults.Jitter.Std()
	if j <= 0 {
		return 0
	}
	return rand.N(j)
}
