package limit

import (
	"context"
	"strings"
	"sync"
)

type RejectReason string

const (
	RejectNone                RejectReason = ""
	RejectConcurrencyExceeded RejectReason = "concurrency_rejected"
	RejectQueueSaturated      RejectReason = "queue_saturated"
)

type Config struct {
	MaxConcurrent int
	MaxQueued     int
}

type Limiter struct {
	mu     sync.Mutex
	states map[string]*state
	cfg    Config
}

type state struct {
	inflight int
	queued   int
	notify   chan struct{}
}

type Lease struct {
	limiter *Limiter
	key     string
	once    sync.Once
}

func New(cfg Config) *Limiter {
	if cfg.MaxConcurrent <= 0 {
		return nil
	}
	if cfg.MaxQueued < 0 {
		cfg.MaxQueued = 0
	}
	return &Limiter{
		states: make(map[string]*state),
		cfg:    cfg,
	}
}

func (l *Limiter) Acquire(ctx context.Context, key string) (*Lease, RejectReason) {
	if l == nil {
		return nil, RejectNone
	}
	key = normalizeKey(key)
	l.mu.Lock()
	st := l.stateForKeyLocked(key)
	if st.inflight < l.cfg.MaxConcurrent {
		st.inflight++
		l.mu.Unlock()
		return &Lease{limiter: l, key: key}, RejectNone
	}
	if l.cfg.MaxQueued <= 0 {
		l.mu.Unlock()
		return nil, RejectConcurrencyExceeded
	}
	if st.queued >= l.cfg.MaxQueued {
		l.mu.Unlock()
		return nil, RejectQueueSaturated
	}
	st.queued++
	l.mu.Unlock()

	for {
		l.mu.Lock()
		st = l.stateForKeyLocked(key)
		ch := st.notify
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			l.mu.Lock()
			st.queued--
			l.cleanupLocked(key, st)
			l.mu.Unlock()
			return nil, RejectConcurrencyExceeded
		case <-ch:
		}

		l.mu.Lock()
		st = l.stateForKeyLocked(key)
		if st.inflight < l.cfg.MaxConcurrent {
			st.queued--
			st.inflight++
			l.mu.Unlock()
			return &Lease{limiter: l, key: key}, RejectNone
		}
		l.mu.Unlock()
	}
}

func (l *Limiter) Counts(key string) (int, int) {
	if l == nil {
		return 0, 0
	}
	key = normalizeKey(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.states[key]
	if st == nil {
		return 0, 0
	}
	return st.inflight, st.queued
}

func (l *Limiter) stateForKeyLocked(key string) *state {
	st := l.states[key]
	if st == nil {
		st = &state{notify: make(chan struct{})}
		l.states[key] = st
	}
	return st
}

func (l *Limiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	st := l.states[key]
	if st == nil {
		return
	}
	if st.inflight > 0 {
		st.inflight--
	}
	close(st.notify)
	st.notify = make(chan struct{})
	l.cleanupLocked(key, st)
}

func (l *Limiter) cleanupLocked(key string, st *state) {
	if st.inflight == 0 && st.queued == 0 {
		delete(l.states, key)
	}
}

func (l *Lease) Release() {
	if l == nil || l.limiter == nil {
		return
	}
	l.once.Do(func() {
		l.limiter.release(l.key)
	})
}

func normalizeKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "global"
	}
	return key
}
