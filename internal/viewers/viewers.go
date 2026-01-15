package viewers

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Protocol string

const (
	ProtocolHLS     Protocol = "hls"
	ProtocolIcecast Protocol = "icecast"
)

type ProtocolCounts struct {
	HLS     int `json:"hls"`
	Icecast int `json:"icecast"`
}

const (
	defaultHLSTTL       = 45 * time.Second
	defaultIcecastTTL   = 0
	defaultCleanupEvery = 30 * time.Second
)

type tracker struct {
	mu           sync.Mutex
	entries      map[Protocol]map[string]*viewerEntry
	ttl          map[Protocol]time.Duration
	lastCleanup  time.Time
	cleanupEvery time.Duration
	hashSalt     []byte
}

type viewerEntry struct {
	lastSeen time.Time
	active   int
}

var defaultTracker = newTracker()

func TrackRequest(protocol Protocol, r *http.Request) {
	defaultTracker.trackRequest(protocol, r)
}

func TrackConnection(protocol Protocol, r *http.Request) func() {
	return defaultTracker.trackConnection(protocol, r)
}

func Counts() ProtocolCounts {
	return defaultTracker.counts()
}

func newTracker() *tracker {
	return &tracker{
		entries: map[Protocol]map[string]*viewerEntry{
			ProtocolHLS:     {},
			ProtocolIcecast: {},
		},
		ttl: map[Protocol]time.Duration{
			ProtocolHLS:     parseDurationEnv("VIEWER_TTL_HLS", defaultHLSTTL),
			ProtocolIcecast: parseDurationEnv("VIEWER_TTL_ICECAST", defaultIcecastTTL),
		},
		cleanupEvery: defaultCleanupEvery,
		hashSalt:     []byte(os.Getenv("VIEWER_HASH_SALT")),
	}
}

func (t *tracker) trackRequest(protocol Protocol, r *http.Request) {
	if r == nil {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return
	}
	ip := clientIP(r)
	if ip == "" {
		return
	}
	hash := t.hashIP(ip)
	if hash == "" {
		return
	}

	now := time.Now()
	t.mu.Lock()
	entry := t.getEntry(protocol, hash)
	entry.lastSeen = now
	t.maybeCleanupLocked(now)
	t.mu.Unlock()
}

func (t *tracker) trackConnection(protocol Protocol, r *http.Request) func() {
	if r == nil {
		return func() {}
	}
	if r.Method != http.MethodGet {
		return func() {}
	}
	ip := clientIP(r)
	if ip == "" {
		return func() {}
	}
	hash := t.hashIP(ip)
	if hash == "" {
		return func() {}
	}

	now := time.Now()
	t.mu.Lock()
	entry := t.getEntry(protocol, hash)
	entry.active++
	entry.lastSeen = now
	t.maybeCleanupLocked(now)
	t.mu.Unlock()

	return func() {
		now := time.Now()
		t.mu.Lock()
		entry := t.entries[protocol][hash]
		if entry != nil {
			if entry.active > 0 {
				entry.active--
			}
			if entry.active <= 0 && t.ttl[protocol] <= 0 {
				delete(t.entries[protocol], hash)
			} else {
				entry.lastSeen = now
			}
		}
		t.maybeCleanupLocked(now)
		t.mu.Unlock()
	}
}

func (t *tracker) counts() ProtocolCounts {
	now := time.Now()
	t.mu.Lock()
	hls := t.countLocked(ProtocolHLS, now)
	icecast := t.countLocked(ProtocolIcecast, now)
	t.mu.Unlock()
	return ProtocolCounts{
		HLS:     hls,
		Icecast: icecast,
	}
}

func (t *tracker) countLocked(protocol Protocol, now time.Time) int {
	entries := t.entries[protocol]
	ttl := t.ttl[protocol]
	count := 0

	for hash, entry := range entries {
		if entry == nil {
			delete(entries, hash)
			continue
		}
		if entry.active > 0 {
			count++
			continue
		}
		if ttl <= 0 {
			delete(entries, hash)
			continue
		}
		if now.Sub(entry.lastSeen) <= ttl {
			count++
		} else {
			delete(entries, hash)
		}
	}

	return count
}

func (t *tracker) getEntry(protocol Protocol, hash string) *viewerEntry {
	entries := t.entries[protocol]
	if entries == nil {
		entries = map[string]*viewerEntry{}
		t.entries[protocol] = entries
	}
	entry := entries[hash]
	if entry == nil {
		entry = &viewerEntry{}
		entries[hash] = entry
	}
	return entry
}

func (t *tracker) maybeCleanupLocked(now time.Time) {
	if t.cleanupEvery <= 0 {
		return
	}
	if !t.lastCleanup.IsZero() && now.Sub(t.lastCleanup) < t.cleanupEvery {
		return
	}
	for protocol := range t.entries {
		t.countLocked(protocol, now)
	}
	t.lastCleanup = now
}

func (t *tracker) hashIP(ip string) string {
	if ip == "" {
		return ""
	}
	h := sha256.New()
	if len(t.hashSalt) > 0 {
		_, _ = h.Write(t.hashSalt)
	}
	_, _ = h.Write([]byte(ip))
	return hex.EncodeToString(h.Sum(nil))
}

func clientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := r.Header.Get("Forwarded"); forwarded != "" {
		if ip := parseForwardedFor(forwarded); ip != "" {
			return ip
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := headerFirst(xff); ip != "" {
			return ip
		}
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		if ip := normalizeIP(xr); ip != "" {
			return ip
		}
	}

	return normalizeIP(r.RemoteAddr)
}

func parseForwardedFor(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(value, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		for _, pair := range strings.Split(part, ";") {
			pair = strings.TrimSpace(pair)
			if !strings.HasPrefix(strings.ToLower(pair), "for=") {
				continue
			}
			raw := strings.TrimSpace(pair[4:])
			raw = strings.Trim(raw, "\"")
			raw = strings.TrimPrefix(raw, "[")
			raw = strings.TrimSuffix(raw, "]")
			if ip := normalizeIP(raw); ip != "" {
				return ip
			}
		}
		break
	}
	return ""
}

func headerFirst(value string) string {
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, ","); idx >= 0 {
		value = value[:idx]
	}
	return normalizeIP(value)
}

func normalizeIP(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	return ""
}

func parseDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil {
		if d <= 0 {
			return 0
		}
		return d
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}

	return fallback
}
