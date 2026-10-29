package main

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	rateBurst      = 10
	maxLimiterKeys = 100_000
	limiterSweep   = time.Minute
)

// defaultTrustedProxies covers loopback and private ranges: the service is not published
// directly, so its only peers are the reverse proxy on a Docker/private network.
const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

func parsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			a, aerr := netip.ParseAddr(part)
			if aerr != nil {
				return nil, err
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// clientAddr returns the caller's address. X-Forwarded-For is honoured only when the direct
// peer is a trusted proxy, and is read right to left so that client-supplied (spoofed) entries
// on the left are ignored: the first address not belonging to a trusted proxy wins.
func clientAddr(r *http.Request, trusted []netip.Prefix) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	peer = peer.Unmap()
	if !inPrefixes(peer, trusted) {
		return peer
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer // malformed chain: fall back to the proxy rather than trust it
		}
		a = a.Unmap()
		if !inPrefixes(a, trusted) {
			return a
		}
	}
	return peer
}

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a per-address token bucket.
type limiter struct {
	rate  float64 // tokens per second
	burst float64
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[netip.Addr]*bucket
	lastSweep time.Time
}

func newLimiter(perMinute int) *limiter {
	return &limiter{
		rate:    float64(perMinute) / 60,
		burst:   rateBurst,
		now:     time.Now,
		buckets: map[netip.Addr]*bucket{},
	}
}

func limiterKey(a netip.Addr) netip.Addr {
	if a.Is6() {
		return netip.PrefixFrom(a, 64).Masked().Addr() // one host can own a whole /64
	}
	return a
}

// allow reports whether a request from a may proceed; if not, retry is the wait in seconds.
func (l *limiter) allow(a netip.Addr) (ok bool, retry int) {
	key := limiterKey(a)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) > limiterSweep || len(l.buckets) >= maxLimiterKeys {
		l.sweep(now)
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, int((1-b.tokens)/l.rate) + 1
}

// sweep drops buckets that have refilled completely (they carry no information).
func (l *limiter) sweep(now time.Time) {
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

func rateLimit(l *limiter, trusted []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.allow(clientAddr(r, trusted)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
