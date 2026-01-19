package observer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	ledgerv1 "ledger-sync/gen/go/ledger/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Observer struct {
	store *Store
	bc    *Broadcaster

	offlineAfter time.Duration
}

func New(store *Store, bc *Broadcaster) *Observer {
	return &Observer{
		store:        store,
		bc:           bc,
		offlineAfter: 6 * time.Second,
	}
}

func (o *Observer) OfflineAfter() time.Duration { return o.offlineAfter }
func (o *Observer) SetOfflineAfter(d time.Duration) {
	if d <= 0 {
		d = 6 * time.Second
	}
	o.offlineAfter = d
}

func (o *Observer) Run(ctx context.Context, seeds []string) error {
	seedQ := make(chan string, 256)

	enqueue := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}
		select {
		case seedQ <- addr:
		default:
		}
	}
	for _, s := range seeds {
		enqueue(s)
	}

	// Periodic snapshot tick (so UI updates lag/status even without events).
	go func() {
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				o.bc.Publish(o.store.Snapshot(o.offlineAfter))
			}
		}
	}()

	// Small worker pool for connecting.
	for i := 0; i < 8; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case addr := <-seedQ:
					_ = o.connectAndStream(ctx, addr, enqueue)
				}
			}
		}()
	}

	<-ctx.Done()
	return ctx.Err()
}

func (o *Observer) connectAndStream(ctx context.Context, addr string, enqueue func(string)) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	o.store.UpsertNode(addr, "")

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		dialCtx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		o.store.MarkOffline(addr)
		return err
	}
	defer conn.Close()

	obsClient := ledgerv1.NewObserverServiceClient(conn)
	nodeClient := ledgerv1.NewNodeServiceClient(conn)

	// Snapshot info + connected peers (best effort; also retried later).
	fetchInfo := func() {
		infoCtx, infoCancel := context.WithTimeout(ctx, 3*time.Second)
		info, err := obsClient.GetNodeInfo(infoCtx, &ledgerv1.GetNodeInfoRequest{})
		infoCancel()
		if err != nil || info == nil {
			return
		}

		o.store.UpsertNode(addr, info.GetNodeId())

		var tipHash []byte
		var tipHeight uint64
		if info.GetSummary() != nil {
			tipHash = info.GetSummary().GetTipHash()
			tipHeight = info.GetSummary().GetTipHeight()
		}
		o.store.UpdateNodeInfo(addr, info.GetProtocolVersion(), info.GetSoftwareVersion(), tipHeight, tipHash)

		// Capture current connection edges so the dashboard is correct even if PeerUp
		// happened before we subscribed.
		for _, p := range info.GetConnectedPeers() {
			if p == nil || strings.TrimSpace(p.GetAddr()) == "" {
				continue
			}
			o.store.SetEdge(addr, info.GetNodeId(), p.GetAddr(), p.GetNodeId(), true)
			enqueue(p.GetAddr())
		}

		o.bc.Publish(o.store.Snapshot(o.offlineAfter))
	}
	fetchInfo()

	// Peer discovery via handshake (best effort).
	hsCtx, hsCancel := context.WithTimeout(ctx, 3*time.Second)
	hs, err := nodeClient.Handshake(hsCtx, &ledgerv1.HandshakeRequest{
		ProtocolVersion: 1,
		SoftwareVersion: "observer",
		NodeId:          "observer",
		ListenAddr:      "",
	})
	hsCancel()
	if err == nil && hs != nil {
		for _, p := range hs.GetPeers() {
			if p == nil {
				continue
			}
			enqueue(p.GetAddr())
		}
	}

	// Stream events.
	stream, err := obsClient.StreamNodeEvents(ctx, &ledgerv1.StreamNodeEventsRequest{})
	if err != nil {
		o.store.MarkOffline(addr)
		o.bc.Publish(o.store.Snapshot(o.offlineAfter))
		return err
	}

	// Periodically refresh node info to keep protocol/software and connection edges populated.
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				fetchInfo()
			}
		}
	}()

	for {
		evResp, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				o.store.MarkOffline(addr)
				o.bc.Publish(o.store.Snapshot(o.offlineAfter))
				return nil
			}
			o.store.MarkOffline(addr)
			o.bc.Publish(o.store.Snapshot(o.offlineAfter))
			return fmt.Errorf("stream %s: %w", addr, err)
		}
		if evResp == nil || evResp.Event == nil {
			o.store.MarkSeen(addr)
			continue
		}
		o.applyNodeEvent(addr, evResp.Event, enqueue)
		o.bc.Publish(o.store.Snapshot(o.offlineAfter))
	}
}

func (o *Observer) applyNodeEvent(addr string, ev *ledgerv1.NodeEvent, enqueue func(string)) {
	o.store.MarkSeen(addr)
	from := o.store.UpsertNode(addr, "")
	fromID := ""
	if from != nil {
		fromID = from.ID
	}

	switch e := ev.Event.(type) {
	case *ledgerv1.NodeEvent_PeerUp:
		if e.PeerUp != nil && e.PeerUp.Peer != nil {
			p := e.PeerUp.Peer
			o.store.SetEdge(addr, fromID, p.GetAddr(), p.GetNodeId(), true)
			enqueue(p.GetAddr())
		}
	case *ledgerv1.NodeEvent_PeerDown:
		if e.PeerDown != nil && e.PeerDown.Peer != nil {
			p := e.PeerDown.Peer
			o.store.SetEdge(addr, fromID, p.GetAddr(), p.GetNodeId(), false)
		}
	case *ledgerv1.NodeEvent_LedgerTipChanged:
		if e.LedgerTipChanged != nil {
			o.store.UpdateNodeInfo(addr, 0, "", e.LedgerTipChanged.GetNewHeight(), e.LedgerTipChanged.GetNewHash())
		}
	case *ledgerv1.NodeEvent_BlocksReceived:
		if e.BlocksReceived != nil {
			o.store.AddBlocksReceived(addr, e.BlocksReceived.GetCount())
			if e.BlocksReceived.Peer != nil {
				enqueue(e.BlocksReceived.Peer.GetAddr())
			}
		}
	case *ledgerv1.NodeEvent_BlocksSent:
		if e.BlocksSent != nil {
			o.store.AddBlocksSent(addr, e.BlocksSent.GetCount())
			if e.BlocksSent.Peer != nil {
				enqueue(e.BlocksSent.Peer.GetAddr())
			}
		}
	default:
		// heartbeat or unknown event
	}
}

