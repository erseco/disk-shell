package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const metricsPeriod = 2 * time.Second
const metricsHistoryWindow = time.Minute

type metricsMessage struct {
	Type    string          `json:"type"`
	SentAt  int64           `json:"sentAt"`
	Samples []systemMetrics `json:"samples"`
}

// All clients share one sampler. Socket writes never hold this lock.
type metricsHub struct {
	mu           sync.Mutex
	started      time.Time
	streamID     string
	sequence     uint64
	clients      map[chan systemMetrics]struct{}
	history      []systemMetrics
	wake         chan struct{}
	done         chan struct{}
	slots        chan struct{}
	authenticate func(*http.Request) (*identity, error)
	recheck      time.Duration
}

func newMetricsHub() *metricsHub {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return &metricsHub{started: time.Now(), streamID: hex.EncodeToString(id[:]), clients: make(map[chan systemMetrics]struct{}), done: make(chan struct{}), wake: make(chan struct{}, 1), slots: make(chan struct{}, 16), authenticate: authenticateWithSlot, recheck: time.Minute}
}

// Keep graph time independent of wall-clock corrections, including while idle.
func (h *metricsHub) timestamp() int64 {
	return h.started.UnixMilli() + time.Since(h.started).Milliseconds()
}

var metricsService = newMetricsHub()

func (h *metricsHub) subscribe() (chan systemMetrics, []systemMetrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan systemMetrics, 1)
	h.clients[ch] = struct{}{}
	cutoff := h.timestamp() - metricsHistoryWindow.Milliseconds()
	history := []systemMetrics{}
	for _, sample := range h.history {
		if sample.Timestamp >= cutoff {
			history = append(history, sample)
		}
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
	return ch, history
}
func (h *metricsHub) unsubscribe(ch chan systemMetrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, ch)
}
func (h *metricsHub) publish(sample systemMetrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sequence++
	sample.Sequence = h.sequence
	sample.StreamID = h.streamID
	history := []systemMetrics{}
	for _, old := range h.history {
		if old.Timestamp >= sample.Timestamp-metricsHistoryWindow.Milliseconds() {
			history = append(history, old)
		}
	}
	h.history = append(history, sample)
	if len(h.history) > 31 {
		h.history = h.history[len(h.history)-31:]
	}
	for ch := range h.clients {
		// A slow reader receives the newest sample instead of delaying all clients.
		select {
		case ch <- sample:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- sample:
			default:
			}
		}
	}
}

type metricsSampler struct {
	previous map[string]cpuCounter
	at       time.Time
}

func (s *metricsSampler) cpu(now time.Time, next map[string]cpuCounter) []metricValue {
	previous := s.previous
	if s.at.IsZero() || now.Sub(s.at) > metricsPeriod*2 || !now.After(s.at) {
		previous = nil
	}
	result := cpuUsage(previous, next)
	s.previous = next
	s.at = now
	return result
}
func (h *metricsHub) run(ctx context.Context) {
	defer close(h.done)
	ticker := time.NewTicker(metricsPeriod)
	defer ticker.Stop()
	sampler := metricsSampler{}
	for {
		woke := false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-h.wake:
			woke = true
		}
		h.mu.Lock()
		active := len(h.clients) > 0
		h.mu.Unlock()
		if !active {
			sampler = metricsSampler{}
			continue
		}
		now := time.Now()
		if woke && !sampler.at.IsZero() && now.Sub(sampler.at) < metricsPeriod {
			continue
		}
		sample := systemMetrics{Timestamp: h.timestamp(), CPU: sampler.cpu(now, readCPUCounters("/proc/stat")), RAM: readMemory("/proc/meminfo"), Disks: readVolumes("/proc/mounts")}
		sample.Fans, sample.Temperatures = readSensors("/sys/class/hwmon")
		h.publish(sample)
	}
}
func metricsIndex(w http.ResponseWriter, r *http.Request) { metricsService.serve(w, r) }
func (h *metricsHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed.", 405)
		return
	}
	if !allowedOrigin(r) {
		http.Error(w, "Forbidden.", 403)
		return
	}
	token, protocol, err := synoTokenFromSubprotocol(r)
	if err != nil {
		http.Error(w, "Forbidden.", 403)
		return
	}
	if token != "" {
		r.Header.Set("X-Syno-Token", token)
	}
	if !acquireSlot(h.slots) {
		http.Error(w, "Metrics service busy.", 503)
		return
	}
	defer releaseSlot(h.slots)
	account, err := h.authenticate(r)
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, errSessionDenied) {
			code = http.StatusUnauthorized
		}
		http.Error(w, "Metrics authentication failed.", code)
		return
	}
	header := make(http.Header)
	if protocol != "" {
		header.Set("Sec-WebSocket-Protocol", protocol)
	}
	conn, err := upgrader.Upgrade(w, r, header)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1024)
	_ = conn.SetReadDeadline(time.Now().Add(websocketPongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(websocketPongWait)) })
	done := make(chan struct{})
	go func() { defer close(done); defer conn.Close(); _, _, _ = conn.ReadMessage() }()
	ch, history := h.subscribe()
	defer h.unsubscribe(ch)
	write := func(samples []systemMetrics) error {
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(metricsMessage{Type: "metrics", SentAt: h.timestamp(), Samples: samples})
	}
	if len(history) > 0 {
		if write(history) != nil {
			return
		}
	}
	ping := time.NewTicker(websocketPingPeriod)
	defer ping.Stop()
	recheck := time.NewTicker(h.recheck)
	defer recheck.Stop()
	for {
		select {
		case <-done:
			return
		case <-h.done:
			return
		case <-r.Context().Done():
			return
		case sample := <-ch:
			if write([]systemMetrics{sample}) != nil {
				return
			}
		case <-ping.C:
			if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
				return
			}
		case <-recheck.C:
			current, err := h.authenticate(r)
			if err != nil || current.username != account.username {
				code := websocket.ClosePolicyViolation
				if err != nil && !errors.Is(err, errSessionDenied) {
					code = websocket.CloseTryAgainLater
				}
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, "DSM session validation required"), time.Now().Add(time.Second))
				return
			}
		}
	}
}
