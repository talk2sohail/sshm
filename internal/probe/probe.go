// Package probe answers "can I actually reach this box right now?" fast.
//
// It does a plain TCP connect to the ssh port rather than a full SSH
// handshake. That is the right trade for this tool: it needs no credentials,
// touches no agent, cannot lock an account, and completes in one round trip.
// A green dot therefore means "the port is open", which is exactly the
// question someone is asking before they try to log in.
package probe

import (
	"context"
	"net"
	"sync"
	"time"
)

// Status is the reachability of a host.
type Status uint8

const (
	Unknown  Status = iota // never checked
	Checking               // in flight
	Up                     // TCP connect succeeded
	Down                   // refused, unreachable, or timed out
)

func (s Status) String() string {
	switch s {
	case Checking:
		return "checking"
	case Up:
		return "up"
	case Down:
		return "down"
	default:
		return "unknown"
	}
}

// Result is one completed probe.
type Result struct {
	Alias   string
	Status  Status
	Latency time.Duration
	Err     error
	At      time.Time
}

// Target is what to dial.
type Target struct {
	Alias string
	Addr  string // host:port
}

// Default tuning. The timeout is short on purpose: this is a liveness hint
// shown while the user is reading the list, not a diagnostic.
const (
	DefaultTimeout     = 900 * time.Millisecond
	DefaultConcurrency = 24
	DefaultTTL         = 30 * time.Second
)

// Prober runs bounded-concurrency TCP probes and caches recent results.
//
// It is safe for concurrent use. The zero value is not usable; call New.
type Prober struct {
	timeout time.Duration
	sem     chan struct{}

	mu    sync.Mutex
	cache map[string]Result
	ttl   time.Duration

	// dial is swappable for tests.
	dial func(ctx context.Context, addr string) (net.Conn, error)
}

// New returns a Prober. Zero values select the defaults.
func New(timeout time.Duration, concurrency int, ttl time.Duration) *Prober {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Prober{
		timeout: timeout,
		sem:     make(chan struct{}, concurrency),
		cache:   make(map[string]Result),
		ttl:     ttl,
		dial: func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
	}
}

// Cached returns the last result for alias if it is still fresh.
func (p *Prober) Cached(alias string) (Result, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.cache[alias]
	if !ok || time.Since(r.At) > p.ttl {
		return Result{}, false
	}
	return r, true
}

// Invalidate drops the cached result for alias, forcing the next Run to redial.
func (p *Prober) Invalidate(alias string) {
	p.mu.Lock()
	delete(p.cache, alias)
	p.mu.Unlock()
}

// Run probes targets concurrently and streams results on the returned channel,
// which is closed when every target has been reported.
//
// Targets with a fresh cached result are reported immediately without dialing,
// unless force is true. Cancelling ctx stops further dials; in-flight ones are
// cut short by their own timeout.
func (p *Prober) Run(ctx context.Context, targets []Target, force bool) <-chan Result {
	out := make(chan Result, len(targets))

	var pending []Target
	for _, t := range targets {
		if !force {
			if r, ok := p.Cached(t.Alias); ok {
				out <- r
				continue
			}
		}
		pending = append(pending, t)
	}

	if len(pending) == 0 {
		close(out)
		return out
	}

	var wg sync.WaitGroup
	for _, t := range pending {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()

			select {
			case p.sem <- struct{}{}:
				defer func() { <-p.sem }()
			case <-ctx.Done():
				out <- Result{Alias: t.Alias, Status: Unknown, Err: ctx.Err(), At: time.Now()}
				return
			}

			r := p.probeOne(ctx, t)
			p.mu.Lock()
			p.cache[t.Alias] = r
			p.mu.Unlock()
			out <- r
		}(t)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func (p *Prober) probeOne(ctx context.Context, t Target) Result {
	dialCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	conn, err := p.dial(dialCtx, t.Addr)
	elapsed := time.Since(start)

	if err != nil {
		return Result{Alias: t.Alias, Status: Down, Latency: elapsed, Err: err, At: time.Now()}
	}
	_ = conn.Close()
	return Result{Alias: t.Alias, Status: Up, Latency: elapsed, At: time.Now()}
}
