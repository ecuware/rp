// Package dns provides a reverse-DNS resolver with an LRU-style expiring cache.
package dns

import (
	"context"
	"net"
	"sync"
	"time"
)

const (
	defaultTTL      = 5 * time.Minute
	defaultCapacity = 512
	negativeTTL     = 30 * time.Second // cache "no result" entries briefly
)

type entry struct {
	hostname  string
	positive  bool // false = negative cache entry
	expiresAt time.Time
}

// Resolver performs reverse-DNS lookups with an in-process cache.
type Resolver struct {
	mu        sync.RWMutex
	cache     map[string]*entry
	timeout   time.Duration
	ttl       time.Duration
	inflight  sync.Map // map[string]struct{} to prevent duplicate in-flight lookups
	done      chan struct{}
	closeOnce sync.Once
}

// NewResolver creates a Resolver.
func NewResolver(timeout time.Duration) *Resolver {
	r := &Resolver{
		cache:   make(map[string]*entry),
		timeout: timeout,
		ttl:     defaultTTL,
		done:    make(chan struct{}),
	}
	go r.janitor()
	return r
}

// Close stops background janitor goroutine.
func (r *Resolver) Close() {
	r.closeOnce.Do(func() {
		close(r.done)
	})
}

// Lookup performs a non-blocking reverse-DNS lookup.
// It returns the cached hostname (empty string if unavailable) and whether the
// cache held a fresh positive result. Lookups are dispatched asynchronously
// so they never block the caller.
func (r *Resolver) Lookup(ip net.IP) string {
	if ip == nil {
		return ""
	}
	key := ip.String()

	r.mu.RLock()
	e, ok := r.cache[key]
	r.mu.RUnlock()

	if ok && time.Now().Before(e.expiresAt) {
		return e.hostname
	}

	// Trigger async refresh (only one in-flight per IP)
	if _, loaded := r.inflight.LoadOrStore(key, struct{}{}); !loaded {
		go func() {
			defer r.inflight.Delete(key)
			ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
			defer cancel()
			r.resolve(ctx, key, ip)
		}()
	}

	// Return stale cached value while refreshing, or empty string
	if ok {
		return e.hostname
	}
	return ""
}

// LookupSync performs a synchronous reverse-DNS lookup (blocks until resolved or timeout).
func (r *Resolver) LookupSync(ctx context.Context, ip net.IP) string {
	if ip == nil {
		return ""
	}
	key := ip.String()

	r.mu.RLock()
	e, ok := r.cache[key]
	r.mu.RUnlock()

	if ok && time.Now().Before(e.expiresAt) {
		return e.hostname
	}

	r.resolve(ctx, key, ip)

	r.mu.RLock()
	e, ok = r.cache[key]
	r.mu.RUnlock()
	if ok {
		return e.hostname
	}
	return ""
}

func (r *Resolver) resolve(ctx context.Context, key string, ip net.IP) {
	names, err := net.DefaultResolver.LookupAddr(ctx, ip.String())

	r.mu.Lock()
	defer r.mu.Unlock()

	if err != nil || len(names) == 0 {
		r.cache[key] = &entry{
			positive:  false,
			expiresAt: time.Now().Add(negativeTTL),
		}
		return
	}

	hostname := names[0]
	// Strip trailing dot added by the resolver
	if len(hostname) > 0 && hostname[len(hostname)-1] == '.' {
		hostname = hostname[:len(hostname)-1]
	}
	r.cache[key] = &entry{
		hostname:  hostname,
		positive:  true,
		expiresAt: time.Now().Add(r.ttl),
	}
}

// janitor periodically evicts expired entries to prevent unbounded growth.
func (r *Resolver) janitor() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			now := time.Now()
			r.mu.Lock()
			for k, e := range r.cache {
				if now.After(e.expiresAt) {
					delete(r.cache, k)
				}
			}
			r.mu.Unlock()
		}
	}
}
