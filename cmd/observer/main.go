package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"ledger-sync/internal/observer"
)

//go:embed web/index.html web/assets/*
var webFS embed.FS

func main() {
	var (
		httpAddr = flag.String("http", "127.0.0.1:8080", "http listen address host:port")
		seeds    = flag.String("seeds", "", "comma-separated node addresses host:port")
	)
	flag.Parse()

	seedList := parseCSV(*seeds)
	if len(seedList) == 0 {
		log.Fatalf("missing -seeds (example: -seeds 127.0.0.1:50051)")
	}

	store := observer.NewStore()
	bc := observer.NewBroadcaster()
	obs := observer.New(store, bc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := obs.Run(ctx, seedList); err != nil {
			log.Printf("observer ended: %v", err)
		}
	}()

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed subfs error: %v", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.Snapshot(obs.OfflineAfter()))
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		// Server-Sent Events (SSE)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch, cancel := bc.Subscribe(16)
		defer cancel()

		// Send an immediate snapshot.
		writeSSE(w, store.Snapshot(obs.OfflineAfter()))
		flusher.Flush()

		heartbeat := time.NewTicker(10 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				// keep connection alive
				fmt.Fprintf(w, ": ping\n\n")
				flusher.Flush()
			case snap, ok := <-ch:
				if !ok {
					return
				}
				writeSSE(w, snap)
				flusher.Flush()
			}
		}
	})

	// Static assets + index.
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", fileServer)

	log.Printf("dashboard: http://%s (seeds=%s)", *httpAddr, strings.Join(seedList, ","))
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func writeSSE(w http.ResponseWriter, snap observer.Snapshot) {
	b, _ := json.Marshal(snap)
	fmt.Fprintf(w, "event: snapshot\n")
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func parseCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

