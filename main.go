package main

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultPort      = "8080"
	defaultIface     = "eth0"
	defaultPollMs    = 1000
	historySize      = 60
	maxVnstatTimeout = 10 * time.Second
)

// ---------------------------------------------------------------------------
// Config

type Config struct {
	Port           string
	Interfaces     []string
	PollIntervalMs int
	AlertThreshold float64 // bytes/sec, 0 = disabled
	AuthUser       string
	AuthPass       string
	HistorySize    int
}

func loadConfig() (Config, error) {
	cfg := Config{
		Port:           defaultPort,
		PollIntervalMs: defaultPollMs,
		AlertThreshold: 0,
		HistorySize:    historySize,
	}

	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}

	if v := os.Getenv("POLL_INTERVAL_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.PollIntervalMs = n
		} else {
			log.Printf("invalid POLL_INTERVAL_MS %q, using %d", v, cfg.PollIntervalMs)
		}
	}

	if v := os.Getenv("ALERT_THRESHOLD"); v != "" {
		t, err := parseBytes(v)
		if err != nil {
			log.Printf("invalid ALERT_THRESHOLD %q: %v (disabled)", v, err)
		} else {
			cfg.AlertThreshold = t
		}
	}

	ifaces := os.Getenv("INTERFACES")
	if strings.TrimSpace(ifaces) == "" {
		ifaces = defaultIface
	}
	for _, raw := range strings.Split(ifaces, ",") {
		name := strings.TrimSpace(raw)
		if !validateIfaceName(name) {
			log.Printf("ignoring invalid interface name %q", name)
			continue
		}
		cfg.Interfaces = append(cfg.Interfaces, name)
	}
	if len(cfg.Interfaces) == 0 {
		return cfg, errors.New("no valid interfaces configured")
	}

	cfg.AuthUser = os.Getenv("AUTH_USER")
	cfg.AuthPass = os.Getenv("AUTH_PASS")
	if cfg.AuthUser != "" && cfg.AuthPass == "" {
		return cfg, errors.New("AUTH_USER set but AUTH_PASS is empty")
	}

	return cfg, nil
}

var ifaceNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`)

func validateIfaceName(name string) bool {
	return ifaceNameRe.MatchString(name)
}

// parseBytes parses sizes like "1048576", "1M", "500K", "2G", "1.5MB" into bytes.
func parseBytes(s string) (float64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return 0, errors.New("empty value")
	}
	m := regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([KMGTP]?B?)$`).FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("unrecognized size %q", s)
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	mult := map[string]float64{
		"": 1, "B": 1,
		"K": 1 << 10, "KB": 1 << 10,
		"M": 1 << 20, "MB": 1 << 20,
		"G": 1 << 30, "GB": 1 << 30,
		"T": 1 << 40, "TB": 1 << 40,
		"P": 1 << 50, "PB": 1 << 50,
	}[m[2]]
	if mult == 0 {
		return 0, fmt.Errorf("unknown unit %q", m[2])
	}
	return val * mult, nil
}

// ---------------------------------------------------------------------------
// Monitoring

type devCounter struct {
	Rx uint64
	Tx uint64
}

// IfaceStats is the live snapshot for one interface.
type IfaceStats struct {
	Name      string  `json:"name"`
	RxBytes   uint64  `json:"rx_bytes"`
	TxBytes   uint64  `json:"tx_bytes"`
	RxSpeed   float64 `json:"rx_speed"` // bytes/sec
	TxSpeed   float64 `json:"tx_speed"` // bytes/sec
	Up        bool    `json:"up"`
	OperState string  `json:"operstate"`
	Timestamp int64   `json:"timestamp"`
}

// Sample is one point of the rolling speed history.
type Sample struct {
	T  int64   `json:"t"`
	RX float64 `json:"rx"`
	TX float64 `json:"tx"`
}

// Monitor samples /proc/net/dev on a fixed ticker and keeps per-interface
// state: current counters, speeds, up/down status and a rolling window.
type Monitor struct {
	mu      sync.RWMutex
	ifaces  []string
	last    map[string]*IfaceStats
	history map[string][]Sample
	cap     int
}

func NewMonitor(ifaces []string, interval time.Duration, cap int) *Monitor {
	m := &Monitor{
		ifaces:  ifaces,
		last:    make(map[string]*IfaceStats, len(ifaces)),
		history: make(map[string][]Sample, len(ifaces)),
		cap:     cap,
	}
	for _, name := range ifaces {
		m.history[name] = nil
	}
	go m.loop(interval)
	return m
}

func (m *Monitor) loop(interval time.Duration) {
	m.tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		m.tick()
	}
}

func (m *Monitor) tick() {
	counters, err := readProcNetDev()
	if err != nil {
		log.Printf("readProcNetDev: %v", err)
		return
	}
	now := time.Now().UnixMilli()

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, name := range m.ifaces {
		cur, ok := counters[name]
		if !ok {
			continue
		}
		var rxSpeed, txSpeed float64
		if prev := m.last[name]; prev != nil {
			rxSpeed, txSpeed = computeSpeeds(prev.RxBytes, prev.TxBytes, prev.Timestamp, cur.Rx, cur.Tx, now)
		}

		st := IfaceStats{
			Name:      name,
			RxBytes:   cur.Rx,
			TxBytes:   cur.Tx,
			RxSpeed:   rxSpeed,
			TxSpeed:   txSpeed,
			Up:        isUp(name),
			OperState: operstate(name),
			Timestamp: now,
		}
		m.last[name] = &st
		m.history[name] = appendSample(m.history[name], Sample{T: now, RX: rxSpeed, TX: txSpeed}, m.cap)
	}
}

// appendSample appends to a ring buffer, keeping at most cap samples.
func appendSample(hist []Sample, s Sample, cap int) []Sample {
	hist = append(hist, s)
	if len(hist) > cap {
		hist = hist[len(hist)-cap:]
	}
	return hist
}

// computeSpeeds derives bytes/sec from two counter snapshots. Counter resets
// (reboots, interface restarts) never produce negative speeds.
func computeSpeeds(prevRx, prevTx uint64, prevTs int64, curRx, curTx uint64, now int64) (rxSpeed, txSpeed float64) {
	if now <= prevTs {
		return 0, 0
	}
	dt := float64(now - prevTs)
	if curRx >= prevRx {
		rxSpeed = float64(curRx-prevRx) * 1000 / dt
	}
	if curTx >= prevTx {
		txSpeed = float64(curTx-prevTx) * 1000 / dt
	}
	return rxSpeed, txSpeed
}

// Snapshot returns a copy of the current state.
func (m *Monitor) Snapshot() ([]IfaceStats, map[string][]Sample) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ifaces := make([]IfaceStats, 0, len(m.ifaces))
	for _, name := range m.ifaces {
		if s, ok := m.last[name]; ok {
			ifaces = append(ifaces, *s)
		}
	}
	history := make(map[string][]Sample, len(m.history))
	for k, v := range m.history {
		history[k] = append([]Sample(nil), v...)
	}
	return ifaces, history
}

// readProcNetDev reads counters for every interface in one go.
func readProcNetDev() (map[string]devCounter, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseProcNetDev(f)
}

func parseProcNetDev(r io.Reader) (map[string]devCounter, error) {
	counters := make(map[string]devCounter)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i+1:])
		if len(fields) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(fields[0], 10, 64)
		tx, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		counters[strings.TrimSpace(line[:i])] = devCounter{Rx: rx, Tx: tx}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(counters) == 0 {
		return nil, errors.New("no interfaces found in /proc/net/dev")
	}
	return counters, nil
}

func operstate(name string) string {
	b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "operstate"))
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

func isUp(name string) bool {
	return operstate(name) == "up"
}

// ---------------------------------------------------------------------------
// vnStat history

type vnstatRoot struct {
	Interfaces []struct {
		Name    string        `json:"name"`
		Traffic vnstatTraffic `json:"traffic"`
	} `json:"interfaces"`
}

type vnstatTraffic struct {
	Day   []vnstatPeriod `json:"day"`
	Hour  []vnstatPeriod `json:"hour"`
	Month []vnstatPeriod `json:"month"`
}

type vnstatPeriod struct {
	Rx   uint64 `json:"rx"`
	Tx   uint64 `json:"tx"`
	Date struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	} `json:"date"`
	Time struct {
		Hour int `json:"hour"`
	} `json:"time"`
}

type HistoryEntry struct {
	Label string `json:"label"`
	Rx    uint64 `json:"rx"`
	Tx    uint64 `json:"tx"`
}

func runVnstat() (*vnstatRoot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), maxVnstatTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "vnstat", "--json")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var data vnstatRoot
	if err := json.Unmarshal(output, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

func lastN[T any](entries []T, n int) []T {
	if len(entries) <= n {
		return entries
	}
	return entries[len(entries)-n:]
}

func dayLabel(d vnstatPeriod) string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Date.Year, d.Date.Month, d.Date.Day)
}

func hourLabel(d vnstatPeriod) string {
	return fmt.Sprintf("%04d-%02d-%02d %02d:00", d.Date.Year, d.Date.Month, d.Date.Day, d.Time.Hour)
}

func monthLabel(d vnstatPeriod) string {
	return fmt.Sprintf("%04d-%02d", d.Date.Year, d.Date.Month)
}

func toEntries(periods []vnstatPeriod, label func(vnstatPeriod) string) []HistoryEntry {
	entries := make([]HistoryEntry, 0, len(periods))
	for _, p := range periods {
		entries = append(entries, HistoryEntry{Label: label(p), Rx: p.Rx, Tx: p.Tx})
	}
	return entries
}

// filterHistory returns the entries for the requested range:
//
//	today/24h -> last 24 hours | 5d/7d/30d -> last N days | month -> last 12 months
func filterHistory(t vnstatTraffic, rng string) []HistoryEntry {
	switch rng {
	case "today", "24h":
		return toEntries(lastN(t.Hour, 24), hourLabel)
	case "7d":
		return toEntries(lastN(t.Day, 7), dayLabel)
	case "30d":
		return toEntries(lastN(t.Day, 30), dayLabel)
	case "month":
		return toEntries(lastN(t.Month, 12), monthLabel)
	default: // 5d
		return toEntries(lastN(t.Day, 5), dayLabel)
	}
}

// todayTotals extracts rx/tx for the current day from the vnstat data.
func todayTotals(data *vnstatRoot, name string, now time.Time) (rx, tx uint64, found bool) {
	for _, iface := range data.Interfaces {
		if iface.Name != name {
			continue
		}
		for _, d := range iface.Traffic.Day {
			if d.Date.Year == now.Year() && d.Date.Month == int(now.Month()) && d.Date.Day == now.Day() {
				return d.Rx, d.Tx, true
			}
		}
		return 0, 0, false
	}
	return 0, 0, false
}

// ---------------------------------------------------------------------------
// Speed test

const (
	speedtestDefaultMB      = 512
	speedtestMinMB          = 1
	speedtestMaxMB          = 512
	speedtestPhase          = 5 * time.Second
	speedtestPhaseTimeout   = 45 * time.Second
	speedtestPingTimeout    = 5 * time.Second
	speedtestDownloadURL    = "https://speed.cloudflare.com/__down"
	speedtestUploadURL      = "https://speed.cloudflare.com/__up"
	speedtestChunkSize      = 256 * 1024
	speedtestChunkBytes     = 25 * 1024 * 1024
	speedtestUploadBlockLen = 4 * 1024 * 1024
)

type speedtestPart struct {
	Bps   float64 `json:"bps"` // bytes/sec
	Bytes int64   `json:"bytes"`
	Ms    int64   `json:"ms"`
}

type speedtestResult struct {
	PingMs    int64         `json:"ping_ms"`
	Download  speedtestPart `json:"download"`
	Upload    speedtestPart `json:"upload"`
	Timestamp int64         `json:"timestamp"`
}

// countReader counts the bytes read from an underlying reader. The counter is
// atomic because the HTTP transport may keep reading the body concurrently
// with the caller inspecting the result.
type countReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func (c *countReader) Count() int64 { return c.n.Load() }

// speedFromTransfer converts a transfer (bytes, ms) into bytes/sec.
func speedFromTransfer(bytes int64, ms int64) float64 {
	if ms <= 0 || bytes <= 0 {
		return 0
	}
	return float64(bytes) * 1000 / float64(ms)
}

// speedtestPing measures the round-trip latency to the download endpoint.
func speedtestPing(ctx context.Context) int64 {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, speedtestDownloadURL+"?bytes=0", nil)
	if err != nil {
		return -1
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return time.Since(start).Milliseconds()
}

// speedtestDownload downloads data from Cloudflare in chunks until
// ~speedtestPhase has elapsed or maxBytes have been received, and returns the
// bytes actually received and the elapsed time in ms. Chunks stay below the
// endpoint's per-request limit (Cloudflare rejects __down requests over ~95 MB
// with 403 Forbidden). On a timeout it returns the partial transfer instead of
// failing.
func speedtestDownload(ctx context.Context, maxBytes int64) (int64, int64, error) {
	start := time.Now()
	deadline := time.Now().Add(speedtestPhase)
	buf := make([]byte, speedtestChunkSize)
	var total int64
	for {
		if time.Now().After(deadline) || total >= maxBytes {
			break
		}
		want := speedtestChunkBytes
		if rem := maxBytes - total; rem < want {
			want = rem
		}
		url := fmt.Sprintf("%s?bytes=%d", speedtestDownloadURL, want)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return total, time.Since(start).Milliseconds(), err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return total, time.Since(start).Milliseconds(), err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return total, time.Since(start).Milliseconds(), fmt.Errorf("download endpoint returned %s", resp.Status)
		}
		n, _ := io.CopyBuffer(io.Discard, resp.Body, buf)
		total += n
		resp.Body.Close()
	}
	return total, time.Since(start).Milliseconds(), nil
}

// speedtestUpload sends up to maxBytes of pseudo-random data to Cloudflare for
// ~speedtestPhase and returns the bytes actually sent and the elapsed ms.
func speedtestUpload(ctx context.Context, maxBytes int64) (int64, int64, error) {
	block := make([]byte, speedtestUploadBlockLen)
	rand.New(rand.NewSource(time.Now().UnixNano())).Read(block)

	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		defer pw.Close()
		deadline := time.Now().Add(speedtestPhase)
		var sent int64
		for {
			if time.Now().After(deadline) || sent >= maxBytes {
				return
			}
			n := len(block)
			if remaining := maxBytes - sent; remaining < int64(n) {
				n = int(remaining)
			}
			if _, err := pw.Write(block[:n]); err != nil {
				return
			}
			sent += int64(n)
		}
	}()

	cr := &countReader{r: pr}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, speedtestUploadURL, cr)
	if err != nil {
		return 0, 0, err
	}
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	return cr.Count(), time.Since(start).Milliseconds(), err
}

func (s *apiServer) handleSpeedtest(w http.ResponseWriter, r *http.Request) {
	if !s.speedtestMu.TryLock() {
		http.Error(w, `{"error": "speed test already running"}`, http.StatusConflict)
		return
	}
	defer s.speedtestMu.Unlock()

	mb := speedtestDefaultMB
	if v := r.URL.Query().Get("mb"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= speedtestMinMB && n <= speedtestMaxMB {
			mb = n
		}
	}
	maxBytes := int64(mb) * 1024 * 1024

	pingCtx, pingCancel := context.WithTimeout(r.Context(), speedtestPingTimeout)
	pingMs := speedtestPing(pingCtx)
	pingCancel()

	dlCtx, dlCancel := context.WithTimeout(r.Context(), speedtestPhaseTimeout)
	dlBytes, dlMs, dlErr := speedtestDownload(dlCtx, maxBytes)
	dlCancel()

	upCtx, upCancel := context.WithTimeout(r.Context(), speedtestPhaseTimeout)
	upBytes, upMs, upErr := speedtestUpload(upCtx, maxBytes)
	upCancel()

	result := speedtestResult{
		PingMs:    pingMs,
		Download:  speedtestPart{Bytes: dlBytes, Ms: dlMs, Bps: speedFromTransfer(dlBytes, dlMs)},
		Upload:    speedtestPart{Bytes: upBytes, Ms: upMs, Bps: speedFromTransfer(upBytes, upMs)},
		Timestamp: time.Now().UnixMilli(),
	}

	if dlErr != nil && dlBytes == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "download failed: " + dlErr.Error()})
		return
	}
	if upErr != nil && upBytes == 0 {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "upload failed: " + upErr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// ---------------------------------------------------------------------------
// HTTP

type apiServer struct {
	cfg         Config
	monitor     *Monitor
	speedtestMu sync.Mutex
}

func (s *apiServer) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/interfaces", s.handleInterfaces)
	mux.HandleFunc("/api/realtime", s.handleRealtime)
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/speedtest", s.handleSpeedtest)

	fs := http.FileServer(http.Dir("./public"))
	mux.Handle("/", fs)

	return s.auth(securityHeaders(mux))
}

func (s *apiServer) auth(next http.Handler) http.Handler {
	if s.cfg.AuthUser == "" {
		return next
	}
	user := []byte(s.cfg.AuthUser)
	pass := []byte(s.cfg.AuthPass)
	publicPaths := []string{"/healthz", "/sw.js", "/manifest.webmanifest"}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range publicPaths {
			if r.URL.Path == p {
				next.ServeHTTP(w, r)
				return
			}
		}
		u, p, ok := r.BasicAuth()
		userOK := ok && subtle.ConstantTimeCompare([]byte(u), user) == 1
		passOK := ok && subtle.ConstantTimeCompare([]byte(p), pass) == 1
		if !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="Network Dashboard"`)
			http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *apiServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces":       s.cfg.Interfaces,
		"poll_interval_ms": s.cfg.PollIntervalMs,
		"alert_threshold":  s.cfg.AlertThreshold,
		"history_size":     s.cfg.HistorySize,
	})
}

func (s *apiServer) handleInterfaces(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.Interfaces)
}

func (s *apiServer) handleRealtime(w http.ResponseWriter, r *http.Request) {
	ifaces, history := s.monitor.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"interfaces": ifaces,
		"history":    history,
	})
}

func (s *apiServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "5d"
	}
	data, err := runVnstat()
	if err != nil {
		http.Error(w, `{"error": "failed to run vnstat"}`, http.StatusInternalServerError)
		return
	}

	byName := make(map[string]vnstatTraffic, len(data.Interfaces))
	for _, iface := range data.Interfaces {
		byName[iface.Name] = iface.Traffic
	}

	type ifaceHistory struct {
		Name    string         `json:"name"`
		Entries []HistoryEntry `json:"entries"`
	}
	result := make([]ifaceHistory, 0, len(s.cfg.Interfaces))
	for _, name := range s.cfg.Interfaces {
		entries := []HistoryEntry{}
		if t, ok := byName[name]; ok {
			entries = filterHistory(t, rng)
		}
		result = append(result, ifaceHistory{Name: name, Entries: entries})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range":      rng,
		"interfaces": result,
	})
}

func (s *apiServer) handleSummary(w http.ResponseWriter, r *http.Request) {
	data, err := runVnstat()
	if err != nil {
		http.Error(w, `{"error": "failed to run vnstat"}`, http.StatusInternalServerError)
		return
	}
	live, _ := s.monitor.Snapshot()
	now := time.Now()

	type ifaceSummary struct {
		Name    string  `json:"name"`
		RxToday uint64  `json:"rx_today"`
		TxToday uint64  `json:"tx_today"`
		RxSpeed float64 `json:"rx_speed"`
		TxSpeed float64 `json:"tx_speed"`
		Up      bool    `json:"up"`
	}

	result := make([]ifaceSummary, 0, len(live))
	var totalRxToday, totalTxToday uint64
	var totalRxSpeed, totalTxSpeed float64
	for _, st := range live {
		rxToday, txToday, found := todayTotals(data, st.Name, now)
		_ = found
		totalRxToday += rxToday
		totalTxToday += txToday
		totalRxSpeed += st.RxSpeed
		totalTxSpeed += st.TxSpeed
		result = append(result, ifaceSummary{
			Name:    st.Name,
			RxToday: rxToday,
			TxToday: txToday,
			RxSpeed: st.RxSpeed,
			TxSpeed: st.TxSpeed,
			Up:      st.Up,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at":   now.UnixMilli(),
		"interfaces":     result,
		"total_rx_today": totalRxToday,
		"total_tx_today": totalTxToday,
		"total_rx_speed": totalRxSpeed,
		"total_tx_speed": totalTxSpeed,
	})
}

// ---------------------------------------------------------------------------
// Main

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	interval := time.Duration(cfg.PollIntervalMs) * time.Millisecond
	monitor := NewMonitor(cfg.Interfaces, interval, cfg.HistorySize)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      (&apiServer{cfg: cfg, monitor: monitor}).routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("monitoring interfaces: %v (poll %dms)", cfg.Interfaces, cfg.PollIntervalMs)
		log.Printf("server listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
