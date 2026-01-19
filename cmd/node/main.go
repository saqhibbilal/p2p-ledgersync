package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	ledgerv1 "ledger-sync/gen/go/ledger/v1"
	"ledger-sync/internal/ledger"
	"ledger-sync/internal/node"

	"google.golang.org/grpc"
)

func main() {
	var (
		id         = flag.String("id", "", "node id (required)")
		listen     = flag.String("listen", "127.0.0.1:50051", "gRPC listen address host:port")
		seeds      = flag.String("seeds", "", "comma-separated peer addresses host:port")
		initBlocks = flag.Int("init-blocks", 0, "create N blocks locally at startup (for demo)")
	)
	flag.Parse()

	if strings.TrimSpace(*id) == "" {
		log.Fatalf("missing -id")
	}

	store := ledger.NewMemStore()
	if *initBlocks > 0 {
		if err := seedBlocks(store, *initBlocks); err != nil {
			log.Fatalf("seed blocks error: %v", err)
		}
	}

	n := node.New(*id, *listen, store)

	lis, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen error: %v", err)
	}

	srv := grpc.NewServer()
	ledgerv1.RegisterNodeServiceServer(srv, n)
	ledgerv1.RegisterObserverServiceServer(srv, n)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start dialing seeds after server is up.
	seedAddrs := parseCSV(*seeds)
	if len(seedAddrs) > 0 {
		n.Logf("seeds: %s", strings.Join(seedAddrs, ", "))
		n.PeerManager().EnsurePeers(ctx, seedAddrs)
	}

	go func() {
		n.Logf("listening on %s (tip=%d)", *listen, tip(store))
		if err := srv.Serve(lis); err != nil {
			log.Printf("grpc serve error: %v", err)
			cancel()
		}
	}()

	waitForSignal()
	n.Logf("shutting down")
	srv.GracefulStop()
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

func seedBlocks(store ledger.Store, n int) error {
	if n <= 0 {
		return nil
	}
	gen, ok := store.Get(0)
	if !ok {
		return fmt.Errorf("missing genesis")
	}
	blocks := make([]ledger.Block, 0, n)
	prev := gen
	// Deterministic seeding: any node generating the first N blocks will produce
	// the same chain, so starting with a "partial ledger" is still compatible.
	base := time.Unix(0, 0).UTC()
	for height := 1; height <= n; height++ {
		ts := base.Add(time.Duration(height) * time.Second)
		b := ledger.NewNextBlock(prev, ts, []byte(fmt.Sprintf("block-%d", height)))
		blocks = append(blocks, b)
		prev = b
	}
	return store.ApplyBlocks(blocks)
}

func tip(store ledger.Store) uint64 {
	h, _ := store.Tip()
	return h
}

func waitForSignal() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}

