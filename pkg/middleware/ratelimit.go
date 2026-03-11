package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/time/rate"
)

var rateLimitRejected = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "rate_limit_rejected_total",
		Help: "Total requests rejected by rate limiter",
	},
)

func init() {
	prometheus.MustRegister(rateLimitRejected)
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiter implements per-IP token-bucket rate limiting.
type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     rate.Limit
	burst    int
}

func NewRateLimiter(r float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate.Limit(r),
		burst:    burst,
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[ip]
	if !ok {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(time.Minute)
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Middleware wraps an http.Handler with per-IP rate limiting.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		limiter := rl.getVisitor(ip)
		if !limiter.Allow() {
			rateLimitRejected.Inc()
			http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}
