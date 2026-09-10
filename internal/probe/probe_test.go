package probe

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// listenAddr starts a throwaway TCP listener and returns its address.
func listenAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return ln.Addr().String()
}

// closedAddr returns an address that is guaranteed to refuse connections.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func collect(ch <-chan Result) map[string]Result {
	out := map[string]Result{}
	for r := range ch {
		out[r.Alias] = r
	}
	return out
}

func TestProbeUpAndDown(t *testing.T) {
	p := New(2*time.Second, 4, DefaultTTL)
	results := collect(p.Run(context.Background(), []Target{
		{Alias: "open", Addr: listenAddr(t)},
		{Alias: "closed", Addr: closedAddr(t)},
	}, false))

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if got := results["open"].Status; got != Up {
		t.Errorf("open status = %v, want up (err: %v)", got, results["open"].Err)
	}
	if got := results["closed"].Status; got != Down {
		t.Errorf("closed status = %v, want down", got)
	}
	if results["closed"].Err == nil {
		t.Error("a down result should carry the dial error")
	}
	if results["open"].Latency <= 0 {
		t.Error("expected a measured latency")
	}
}

func TestChannelClosesWithNoTargets(t *testing.T) {
	p := New(0, 0, 0)
	ch := p.Run(context.Background(), nil, false)
	select {
	case _, open := <-ch:
		if open {
			t.Error("unexpected result on an empty run")
		}
	case <-time.After(time.Second):
		t.Fatal("channel was never closed")
	}
}

func TestTimeoutIsReportedAsDown(t *testing.T) {
	p := New(30*time.Millisecond, 4, DefaultTTL)
	// 203.0.113.0/24 is TEST-NET-3: reserved for documentation, never routed,
	// so a dial there hangs until the timeout rather than being refused.
	results := collect(p.Run(context.Background(), []Target{
		{Alias: "blackhole", Addr: "203.0.113.1:22"},
	}, false))

	r := results["blackhole"]
	if r.Status != Down {
		t.Errorf("status = %v, want down", r.Status)
	}
	if r.Latency > 2*time.Second {
		t.Errorf("latency = %v; the timeout was not honoured", r.Latency)
	}
}

func TestCacheAvoidsRedial(t *testing.T) {
	p := New(time.Second, 4, time.Minute)

	var mu sync.Mutex
	dials := 0
	p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return nil, errors.New("refused")
	}

	targets := []Target{{Alias: "a", Addr: "127.0.0.1:1"}}
	collect(p.Run(context.Background(), targets, false))
	collect(p.Run(context.Background(), targets, false))

	mu.Lock()
	defer mu.Unlock()
	if dials != 1 {
		t.Errorf("dialled %d times, want 1 (second call should hit the cache)", dials)
	}
}

func TestForceBypassesCache(t *testing.T) {
	p := New(time.Second, 4, time.Minute)

	var mu sync.Mutex
	dials := 0
	p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		dials++
		mu.Unlock()
		return nil, errors.New("refused")
	}

	targets := []Target{{Alias: "a", Addr: "127.0.0.1:1"}}
	collect(p.Run(context.Background(), targets, false))
	collect(p.Run(context.Background(), targets, true))

	mu.Lock()
	defer mu.Unlock()
	if dials != 2 {
		t.Errorf("dialled %d times, want 2 (force must redial)", dials)
	}
}

func TestCacheExpires(t *testing.T) {
	p := New(time.Second, 4, 10*time.Millisecond)
	p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		return nil, errors.New("refused")
	}
	targets := []Target{{Alias: "a", Addr: "127.0.0.1:1"}}
	collect(p.Run(context.Background(), targets, false))

	if _, ok := p.Cached("a"); !ok {
		t.Fatal("result should be cached immediately")
	}
	time.Sleep(25 * time.Millisecond)
	if _, ok := p.Cached("a"); ok {
		t.Error("cache entry should have expired")
	}
}

func TestInvalidate(t *testing.T) {
	p := New(time.Second, 4, time.Minute)
	p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		return nil, errors.New("refused")
	}
	targets := []Target{{Alias: "a", Addr: "127.0.0.1:1"}}
	collect(p.Run(context.Background(), targets, false))
	p.Invalidate("a")
	if _, ok := p.Cached("a"); ok {
		t.Error("entry should be gone after Invalidate")
	}
}

func TestConcurrencyIsBounded(t *testing.T) {
	const limit = 3
	p := New(time.Second, limit, DefaultTTL)

	var mu sync.Mutex
	inFlight, peak := 0, 0
	p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil, errors.New("refused")
	}

	targets := make([]Target, 30)
	for i := range targets {
		targets[i] = Target{Alias: string(rune('a' + i)), Addr: "127.0.0.1:1"}
	}
	collect(p.Run(context.Background(), targets, false))

	mu.Lock()
	defer mu.Unlock()
	if peak > limit {
		t.Errorf("peak concurrency = %d, want <= %d", peak, limit)
	}
	if peak < 2 {
		t.Errorf("peak concurrency = %d; probes did not run in parallel", peak)
	}
}

func TestCancellationStillClosesChannel(t *testing.T) {
	p := New(5*time.Second, 2, DefaultTTL)
	p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	targets := make([]Target, 20)
	for i := range targets {
		targets[i] = Target{Alias: string(rune('a' + i)), Addr: "127.0.0.1:1"}
	}
	ch := p.Run(ctx, targets, false)
	cancel()

	done := make(chan struct{})
	go func() {
		collect(ch)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run leaked: channel never closed after cancellation")
	}
}

func TestStatusString(t *testing.T) {
	for s, want := range map[Status]string{
		Unknown: "unknown", Checking: "checking", Up: "up", Down: "down",
	} {
		if got := s.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", s, got, want)
		}
	}
}
