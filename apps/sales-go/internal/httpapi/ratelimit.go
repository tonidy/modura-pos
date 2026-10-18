package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter is a token bucket per client IP, applied to commands only. Every demo
// visitor shares the cashier identity, so limiting per identity would throttle
// everyone together (plan section 5).
type ipLimiter struct {
	header string
	limit  rate.Limit
	burst  int

	mu        sync.Mutex
	clients   map[string]*client
	lastSweep time.Time
}

type client struct {
	limiter *rate.Limiter
	seen    time.Time
}

func newIPLimiter(header string, limit rate.Limit, burst int) *ipLimiter {
	return &ipLimiter{header: header, limit: limit, burst: burst, clients: map[string]*client{}}
}

func (l *ipLimiter) allow(r *http.Request) bool {
	ip := l.clientIP(r)
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > time.Minute {
		for key, c := range l.clients {
			if now.Sub(c.seen) > 10*time.Minute {
				delete(l.clients, key)
			}
		}
		l.lastSweep = now
	}
	c, ok := l.clients[ip]
	if !ok {
		c = &client{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.clients[ip] = c
	}
	c.seen = now
	return c.limiter.Allow()
}

// clientIP trusts the configured header only. It must be one the hosting platform's
// proxy sets and overwrites (Fly-Client-IP on Fly.io), or clients could pick their
// own bucket. For X-Forwarded-For the last entry is used, since that is the one the
// trusted proxy appended. Without a header, the TCP peer address is used.
func (l *ipLimiter) clientIP(r *http.Request) string {
	if l.header != "" {
		if value := r.Header.Get(l.header); value != "" {
			parts := strings.Split(value, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *ipLimiter) retryAfterSeconds() int {
	if l.limit <= 0 {
		return 60
	}
	return max(1, int(1/float64(l.limit)+0.5))
}
