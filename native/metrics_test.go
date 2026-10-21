package main

import (
	"errors"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMetricsCPUDeltas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stat")
	os.WriteFile(path, []byte("cpu 100 0 0 100\ncpu0 100 0 20 80 10 0 0 0 30 0\ncpu2 1 0 0 9\n"), 0600)
	next := readCPUCounters(path)
	if next["cpu0"].total != 210 {
		t.Fatal("guest time double counted", next)
	}
	if len(cpuUsage(nil, next)) != 0 {
		t.Fatal("first sample must be unavailable")
	}
	usage := cpuUsage(map[string]cpuCounter{"cpu0": {total: 110, idle: 40}}, next)
	if len(usage) != 1 || usage[0].Value != 50 {
		t.Fatal(usage)
	}
	if len(cpuUsage(map[string]cpuCounter{"cpu0": {total: 999, idle: 999}}, next)) != 0 {
		t.Fatal("counter reset")
	}
}
func TestMetricsMemory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	os.WriteFile(path, []byte("MemTotal: 1000 kB\nMemAvailable: 600 kB\n"), 0600)
	if v := readMemory(path); v == nil || *v != 40 {
		t.Fatal(v)
	}
	os.WriteFile(path, []byte("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 100 kB\nCached: 400 kB\n"), 0600)
	if v := readMemory(path); v == nil || *v != 40 {
		t.Fatal(v)
	}
	if readMemory(path+"missing") != nil {
		t.Fatal("missing memory should be unavailable")
	}
}
func TestMetricsSensorFiltering(t *testing.T) {
	root := t.TempDir()
	for i, name := range []string{"coretemp", "drivetemp"} {
		dir := filepath.Join(root, []string{"hwmon0", "hwmon1"}[i])
		os.Mkdir(dir, 0700)
		os.WriteFile(filepath.Join(dir, "name"), []byte(name), 0600)
		os.WriteFile(filepath.Join(dir, "temp1_input"), []byte("42000"), 0600)
		os.WriteFile(filepath.Join(dir, "fan1_input"), []byte("0"), 0600)
		os.WriteFile(filepath.Join(dir, "fan2_input"), []byte("1200"), 0600)
		os.WriteFile(filepath.Join(dir, "fan2_fault"), []byte("1"), 0600)
	}
	fans, temps := readSensors(root)
	if len(fans) != 2 || fans[0].Value != 0 {
		t.Fatal("stopped fans must remain valid, faulty readings omitted", fans)
	}
	if len(temps) != 1 || temps[0].Value != 42 {
		t.Fatal("only drive temperatures", temps)
	}
}

func TestMetricsEndpointSecurity(t *testing.T) {
	configureDevelopmentAuthentication(t, writeTestExecutable(t, "auth", "#!/bin/sh\nexit 1\n"), writeTestExecutable(t, "id", "#!/bin/sh\nexit 1\n"))
	for _, tc := range []struct {
		method, origin string
		status         int
	}{
		{"POST", "", 405}, {"GET", "http://attacker.example", 403}, {"GET", "http://dsm.local", 503},
	} {
		r := httptest.NewRequest(tc.method, "http://dsm.local/diskshell/metrics", nil)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		metricsIndex(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d, want %d", tc.method, tc.origin, w.Code, tc.status)
		}
	}
}

func TestMetricsSamplerRejectsLongIntervals(t *testing.T) {
	s := metricsSampler{}
	at := time.Now()
	if len(s.cpu(at, map[string]cpuCounter{"cpu0": {total: 100, idle: 50}})) != 0 {
		t.Fatal("initial sample")
	}
	if got := s.cpu(at.Add(2*time.Second), map[string]cpuCounter{"cpu0": {total: 200, idle: 100}}); len(got) != 1 || got[0].Value != 50 {
		t.Fatal(got)
	}
	if got := s.cpu(at.Add(30*time.Minute), map[string]cpuCounter{"cpu0": {total: 10000, idle: 9000}}); len(got) != 0 {
		t.Fatal("stale interval reported as live", got)
	}
}
func TestMetricsHubSlowReadersAndHistory(t *testing.T) {
	h := newMetricsHub()
	slow, _ := h.subscribe()
	fast, _ := h.subscribe()
	defer h.unsubscribe(slow)
	defer h.unsubscribe(fast)
	at := time.Now().UnixMilli()
	for i := 0; i < 40; i++ {
		h.publish(systemMetrics{Timestamp: at + int64(i)*2000})
		<-fast
	}
	if got := <-slow; got.Timestamp != at+78000 {
		t.Fatal("slow reader did not receive latest")
	}
	if len(h.history) != 31 {
		t.Fatal("unbounded history", len(h.history))
	}
}
func TestMetricsWebSocketRevalidatesAndSharesSamples(t *testing.T) {
	h := newMetricsHub()
	h.recheck = 80 * time.Millisecond
	var calls atomic.Int32
	h.authenticate = func(*http.Request) (*identity, error) {
		if calls.Add(1) > 1 {
			return nil, errSessionDenied
		}
		return &identity{username: "admin"}, nil
	}
	server := httptest.NewServer(http.HandlerFunc(h.serve))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		h.mu.Lock()
		ready := len(h.clients) == 1
		h.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not subscribed")
		}
		time.Sleep(time.Millisecond)
	}
	h.publish(systemMetrics{Timestamp: time.Now().UnixMilli(), CPU: []metricValue{{Name: "cpu0", Value: 50}}})
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var message metricsMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatal(err)
	}
	if len(message.Samples) != 1 || message.Samples[0].CPU[0].Value != 50 {
		t.Fatal(message)
	}
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
		t.Fatalf("expired session was not closed: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("unexpected authentication count", calls.Load())
	}
}

func TestMetricsTransientAuthenticationRetries(t *testing.T) {
	h := newMetricsHub()
	h.recheck = time.Millisecond
	var calls atomic.Int32
	h.authenticate = func(*http.Request) (*identity, error) {
		if calls.Add(1) > 1 {
			return nil, errors.New("session lookup timed out")
		}
		return &identity{username: "admin"}, nil
	}
	server := httptest.NewServer(http.HandlerFunc(h.serve))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Origin": []string{server.URL}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _, err = conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseTryAgainLater) {
		t.Fatalf("transient error must allow retry: %v", err)
	}
}
func TestMetricsStreamIdentityAndMonotonicTime(t *testing.T) {
	h := newMetricsHub()
	before := h.timestamp()
	if after := h.timestamp(); after < before {
		t.Fatal("time moved backwards")
	}
	h.publish(systemMetrics{Timestamp: before})
	h.publish(systemMetrics{Timestamp: before + 2000})
	if h.history[0].Sequence != 1 || h.history[1].Sequence != 2 || h.history[0].StreamID != h.streamID {
		t.Fatal("missing stream identity")
	}
	if newMetricsHub().streamID == h.streamID {
		t.Fatal("restart reused stream identity")
	}
}
