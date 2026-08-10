package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RealTimeData struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
	Timestamp int64  `json:"timestamp"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	interfacesEnv := os.Getenv("INTERFACES")
	if interfacesEnv == "" {
		interfacesEnv = "eth0" // Default fallback
	}
	monitoredInterfaces := strings.Split(interfacesEnv, ",")

	// Serve static files
	fs := http.FileServer(http.Dir("./public"))
	http.Handle("/", fs)

	// API to get list of monitored interfaces
	http.HandleFunc("/api/interfaces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(monitoredInterfaces)
	})

	// API for real-time stats
	http.HandleFunc("/api/realtime", func(w http.ResponseWriter, r *http.Request) {
		stats := make(map[string]RealTimeData)
		now := time.Now().UnixMilli()

		for _, iface := range monitoredInterfaces {
			iface = strings.TrimSpace(iface)
			rx, _ := readStat(iface, "rx_bytes")
			tx, _ := readStat(iface, "tx_bytes")
			stats[iface] = RealTimeData{
				RxBytes:   rx,
				TxBytes:   tx,
				Timestamp: now,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	})

	// API for historical stats using vnStat
	http.HandleFunc("/api/history", func(w http.ResponseWriter, r *http.Request) {
		cmd := exec.Command("vnstat", "--json")
		output, err := cmd.Output()
		if err != nil {
			http.Error(w, `{"error": "Failed to run vnstat"}`, http.StatusInternalServerError)
			return
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.Write(output)
	})

	log.Printf("Server listening on port %s...", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

func readStat(iface, statName string) (uint64, error) {
	path := filepath.Join("/sys/class/net", iface, "statistics", statName)
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	
	valStr := strings.TrimSpace(string(content))
	val, err := strconv.ParseUint(valStr, 10, 64)
	if err != nil {
		return 0, err
	}
	return val, nil
}
