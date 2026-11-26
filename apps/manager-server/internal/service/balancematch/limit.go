package balancematch

import (
	"context"
	"sync"
	"time"
)

// Serialize each host's requests, including across Match calls and ports. Keep
// an entry only while held or cooling down; no goroutines or unbounded queues.
type hostLimiter struct {
	mu    sync.Mutex
	hosts map[string]*hostLease
}

type hostLease struct {
	host  string
	token chan struct{}
	refs  int
	next  time.Time // touched only by the lease holder (or when refs == 0)
}

func (l *hostLimiter) acquire(ctx context.Context, host string) *hostLease {
	l.mu.Lock()
	if l.hosts == nil {
		l.hosts = make(map[string]*hostLease)
	}
	now := time.Now()
	for key, entry := range l.hosts {
		if entry.refs == 0 && !now.Before(entry.next) {
			delete(l.hosts, key)
		}
	}
	e := l.hosts[host]
	if e == nil {
		e = &hostLease{host: host, token: make(chan struct{}, 1)}
		l.hosts[host] = e
	}
	e.refs++
	l.mu.Unlock()
	select {
	case e.token <- struct{}{}:
		return e
	case <-ctx.Done():
		l.drop(e)
		return nil
	}
}

func (l *hostLimiter) release(e *hostLease) {
	<-e.token
	l.drop(e)
}

func (l *hostLimiter) drop(e *hostLease) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.refs--
	if e.refs == 0 && !time.Now().Before(e.next) {
		delete(l.hosts, e.host)
	}
}

func (e *hostLease) wait(ctx context.Context, interval time.Duration) bool {
	if delay := time.Until(e.next); delay > 0 {
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-ctx.Done():
			return false
		}
	}
	if ctx.Err() != nil {
		return false
	}
	e.next = time.Now().Add(interval)
	return true
}
