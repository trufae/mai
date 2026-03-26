package mcplib

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// HTTPSecurity configures HTTP-layer security for MCP servers.
// The zero value is secure: CORS rejects cross-origin requests,
// DNS rebinding is blocked on loopback, and Content-Type is enforced.
type HTTPSecurity struct {
	// AllowedOrigins lists accepted Origin header values for CORS.
	// Empty (default) rejects all cross-origin requests.
	// Use ["*"] to allow any origin (not recommended for public ports).
	AllowedOrigins []string

	// AllowDNSRebinding disables Host header validation on loopback listeners.
	AllowDNSRebinding bool

	// SessionTimeout is the idle duration before SSE sessions expire.
	// Zero means no timeout.
	SessionTimeout time.Duration

	// MaxSessions limits concurrent SSE sessions. Zero means unlimited.
	MaxSessions int

	// RateLimit is the max requests per second per source IP. Zero means unlimited.
	RateLimit float64

	// RateBurst is the token bucket burst size. Defaults to max(1, int(RateLimit)).
	RateBurst int
}

// SetHTTPSecurity configures HTTP security options.
// The zero value is secure: CORS restricted, DNS rebinding blocked, Content-Type enforced.
func (s *MCPServer) SetHTTPSecurity(sec HTTPSecurity) {
	s.httpSecurity = sec
	if sec.RateLimit > 0 {
		s.limiter = newRateLimiter(sec.RateLimit, sec.RateBurst)
	} else {
		s.limiter = nil
	}
}

// httpSecurityCheck runs all security checks on an incoming HTTP request.
// Returns true if the request was rejected (response already written).
func (s *MCPServer) httpSecurityCheck(w http.ResponseWriter, r *http.Request) bool {
	// DNS rebinding protection on loopback listeners
	if !s.httpSecurity.AllowDNSRebinding {
		if laddr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			if isLoopback(laddr.String()) && !isLoopback(r.Host) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return true
			}
		}
	}

	// CORS: validate Origin header
	origin := r.Header.Get("Origin")
	if origin != "" {
		if !s.originAllowed(origin) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return true
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Cache-Control, X-SSE-Session-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Vary", "Origin")
	}

	// CORS preflight
	if r.Method == http.MethodOptions {
		if origin != "" {
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		w.WriteHeader(http.StatusNoContent)
		return true
	}

	// Content-Type enforcement for POST
	if r.Method == http.MethodPost {
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		if !strings.HasPrefix(ct, "application/json") {
			http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
			return true
		}
	}

	// Rate limiting
	if s.limiter != nil && !s.limiter.allow(clientIP(r)) {
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return true
	}

	return false
}

func (s *MCPServer) originAllowed(origin string) bool {
	for _, o := range s.httpSecurity.AllowedOrigins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

// isLoopback reports whether addr refers to a loopback address.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// clientIP extracts the remote IP from the request.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter implements a per-IP token bucket rate limiter.
type rateLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   int
	clients map[string]*rateBucket
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rate float64, burst int) *rateLimiter {
	if burst < 1 {
		burst = int(rate)
		if burst < 1 {
			burst = 1
		}
	}
	return &rateLimiter{
		rate:    rate,
		burst:   burst,
		clients: make(map[string]*rateBucket),
	}
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	// Purge stale entries when map grows large
	if len(rl.clients) > 1000 {
		cutoff := now.Add(-5 * time.Minute)
		for k, v := range rl.clients {
			if v.last.Before(cutoff) {
				delete(rl.clients, k)
			}
		}
	}
	b, ok := rl.clients[ip]
	if !ok {
		rl.clients[ip] = &rateBucket{tokens: float64(rl.burst) - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.rate
	b.last = now
	if b.tokens > float64(rl.burst) {
		b.tokens = float64(rl.burst)
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
