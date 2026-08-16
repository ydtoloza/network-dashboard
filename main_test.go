package main

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
	"time"
)

func TestParseProcNetDev(t *testing.T) {
	input := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 12345   123    0    0    0     0          0         0        6789   123    0    0    0     0       0          0
  eth0: 1000    10     0    0    0     0          0         0        2000    20    0    0    0     0       0          0
`
	counters, err := parseProcNetDev(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(counters) != 2 {
		t.Fatalf("expected 2 interfaces, got %d", len(counters))
	}
	eth0 := counters["eth0"]
	if eth0.Rx != 1000 || eth0.Tx != 2000 {
		t.Errorf("eth0 counters wrong: %+v", eth0)
	}
	lo := counters["lo"]
	if lo.Rx != 12345 || lo.Tx != 6789 {
		t.Errorf("lo counters wrong: %+v", lo)
	}
}

func TestParseProcNetDevEmpty(t *testing.T) {
	if _, err := parseProcNetDev(strings.NewReader("Inter-|   Receive\n face |bytes\n")); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseBytes(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"1048576", 1048576, true},
		{"1M", 1048576, true},
		{"1MB", 1048576, true},
		{"1.5M", 1572864, true},
		{"500K", 512000, true},
		{"2G", 2147483648, true},
		{"1T", 1099511627776, true},
		{"", 0, false},
		{"abc", 0, false},
		{"1Q", 0, false},
	}
	for _, c := range cases {
		got, err := parseBytes(c.in)
		if c.ok != (err == nil) {
			t.Errorf("parseBytes(%q) err=%v, want ok=%v", c.in, err, c.ok)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parseBytes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestValidateIfaceName(t *testing.T) {
	valid := []string{"eth0", "tailscale0", "enp0s3", "br-1234", "wg0", "lo"}
	for _, name := range valid {
		if !validateIfaceName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}
	invalid := []string{"", "eth 0", "eth;rm", "../etc", "eth0/../../x", "averyveryverylonginterfacename123"}
	for _, name := range invalid {
		if validateIfaceName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

func TestAppendSampleCap(t *testing.T) {
	var hist []Sample
	for i := 0; i < 75; i++ {
		hist = appendSample(hist, Sample{T: int64(i), RX: float64(i), TX: float64(i)}, 60)
	}
	if len(hist) != 60 {
		t.Fatalf("expected 60 samples, got %d", len(hist))
	}
	if hist[0].T != 15 {
		t.Errorf("expected oldest sample T=15, got %d", hist[0].T)
	}
	if hist[59].T != 74 {
		t.Errorf("expected newest sample T=74, got %d", hist[59].T)
	}
}

func TestFilterHistory(t *testing.T) {
	now := time.Now()
	mkPeriod := func(year, month, day, hour int) vnstatPeriod {
		p := vnstatPeriod{Rx: 100, Tx: 200}
		p.Date.Year = year
		p.Date.Month = month
		p.Date.Day = day
		p.Time.Hour = hour
		return p
	}
	traffic := vnstatTraffic{
		Hour:  []vnstatPeriod{mkPeriod(now.Year(), int(now.Month()), now.Day(), 10), mkPeriod(now.Year(), int(now.Month()), now.Day(), 11), mkPeriod(now.Year(), int(now.Month()), now.Day(), 12)},
		Day:   []vnstatPeriod{mkPeriod(2026, 8, 10, 0), mkPeriod(2026, 8, 11, 0), mkPeriod(2026, 8, 12, 0), mkPeriod(2026, 8, 13, 0)},
		Month: []vnstatPeriod{mkPeriod(2026, 1, 1, 0), mkPeriod(2026, 2, 1, 0)},
	}

	if got := filterHistory(traffic, "24h"); len(got) != 3 {
		t.Errorf("24h: expected 3 entries, got %d", len(got))
	}
	if got := filterHistory(traffic, "5d"); len(got) != 4 {
		t.Errorf("5d: expected 4 entries, got %d", len(got))
	}
	if got := filterHistory(traffic, "30d"); len(got) != 4 {
		t.Errorf("30d: expected 4 entries, got %d", len(got))
	}
	if got := filterHistory(traffic, "month"); len(got) != 2 {
		t.Errorf("month: expected 2 entries, got %d", len(got))
	}
	labels := filterHistory(traffic, "5d")
	if labels[0].Label != "2026-08-10" {
		t.Errorf("unexpected label %q", labels[0].Label)
	}
}

func TestTodayTotals(t *testing.T) {
	now := time.Now()
	var root vnstatRoot
	root.Interfaces = []struct {
		Name    string        `json:"name"`
		Traffic vnstatTraffic `json:"traffic"`
	}{
		{Name: "eth0", Traffic: vnstatTraffic{Day: []vnstatPeriod{}}},
	}
	// build a day entry for today
	var p vnstatPeriod
	p.Rx = 5000
	p.Tx = 3000
	p.Date.Year = now.Year()
	p.Date.Month = int(now.Month())
	p.Date.Day = now.Day()
	root.Interfaces[0].Traffic.Day = append(root.Interfaces[0].Traffic.Day, p)

	rx, tx, ok := todayTotals(&root, "eth0", now)
	if !ok || rx != 5000 || tx != 3000 {
		t.Errorf("todayTotals = (%d, %d, %v), want (5000, 3000, true)", rx, tx, ok)
	}
	if _, _, ok := todayTotals(&root, "missing", now); ok {
		t.Error("expected not found for unknown interface")
	}
}

func TestComputeSpeed(t *testing.T) {
	rx, tx := computeSpeeds(0, 0, 1000, 1000, 2000, 2000)
	if rx != 1000 || tx != 2000 {
		t.Errorf("expected (1000, 2000), got (%v, %v)", rx, tx)
	}

	// Counter reset (reboot) must not produce negative speeds.
	rx, tx = computeSpeeds(1000, 2000, 1000, 500, 100, 2000)
	if rx != 0 || tx != 0 {
		t.Errorf("expected (0, 0) on counter reset, got (%v, %v)", rx, tx)
	}

	// Same timestamp (first tick) -> 0.
	rx, tx = computeSpeeds(0, 0, 2000, 1000, 2000, 2000)
	if rx != 0 || tx != 0 {
		t.Errorf("expected (0, 0) on first tick, got (%v, %v)", rx, tx)
	}
}

func TestParseBytesMath(t *testing.T) {
	got, err := parseBytes("1.5M")
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got-1572864) > 0.001 {
		t.Errorf("expected 1572864, got %v", got)
	}
}

func TestSpeedFromTransfer(t *testing.T) {
	cases := []struct {
		bytes int64
		ms    int64
		want  float64
	}{
		{0, 1000, 0},
		{1000, 0, 0},
		{1000, 1000, 1000},
		{5000, 2000, 2500},
		{1024, 1024, 1000},
	}
	for _, c := range cases {
		if got := speedFromTransfer(c.bytes, c.ms); got != c.want {
			t.Errorf("speedFromTransfer(%d, %d) = %v, want %v", c.bytes, c.ms, got, c.want)
		}
	}
}

func TestCountReader(t *testing.T) {
	payload := []byte("hello world")
	cr := &countReader{r: bytes.NewReader(payload)}
	buf := make([]byte, 3)
	var total []byte
	for {
		n, err := cr.Read(buf)
		total = append(total, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if string(total) != "hello world" {
		t.Errorf("read %q, want %q", total, payload)
	}
	if cr.Count() != int64(len(payload)) {
		t.Errorf("counted %d bytes, want %d", cr.Count(), len(payload))
	}
}
