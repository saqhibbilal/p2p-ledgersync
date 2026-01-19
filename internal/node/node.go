package node

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	ledgerv1 "ledger-sync/gen/go/ledger/v1"
	"ledger-sync/internal/ledger"

	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Node struct {
	ledgerv1.UnimplementedNodeServiceServer
	ledgerv1.UnimplementedObserverServiceServer

	id         string
	listenAddr string
	store      ledger.Store
	events     *EventBus

	peers *PeerManager
}

func New(id, listenAddr string, store ledger.Store) *Node {
	n := &Node{
		id:         id,
		listenAddr: listenAddr,
		store:      store,
		events:     NewEventBus(),
	}
	n.peers = NewPeerManager(n)
	return n
}

func (n *Node) ID() string         { return n.id }
func (n *Node) ListenAddr() string { return n.listenAddr }
func (n *Node) Store() ledger.Store { return n.store }
func (n *Node) Events() *EventBus  { return n.events }

func (n *Node) PeerManager() *PeerManager { return n.peers }

// --- NodeService ---

func (n *Node) Handshake(ctx context.Context, req *ledgerv1.HandshakeRequest) (*ledgerv1.HandshakeResponse, error) {
	_ = ctx
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	// Learn about the caller so peer discovery works even for nodes that only
	// receive inbound connections.
	if req.GetListenAddr() != "" {
		n.peers.NotePeer(&ledgerv1.Peer{
			NodeId: req.GetNodeId(),
			Addr:   req.GetListenAddr(),
		})
	}

	resp := &ledgerv1.HandshakeResponse{
		ProtocolVersion: ProtocolVersion,
		SoftwareVersion: SoftwareVersion,
		NodeId:          n.id,
		ListenAddr:      n.listenAddr,
		// Return known peers for discovery (not only currently-connected peers).
		Peers:           n.peers.KnownPeersPB(64, ""),
		Summary:         summaryFromStore(n.store),
	}

	return resp, nil
}

func (n *Node) GossipStream(stream ledgerv1.NodeService_GossipStreamServer) error {
	// Server side is passive: it receives peer summaries and can respond with our summary.
	// The actual syncing is driven by the dialing side (client), which can also call GetBlocks.
	ctx := stream.Context()
	peerLabel := "unknown-peer"

	sendSummary := func() {
		msg := &ledgerv1.GossipStreamResponse{
			SentAt: timestamppb.Now(),
			Msg: &ledgerv1.GossipStreamResponse_Summary{
				Summary: summaryFromStore(n.store),
			},
		}
		_ = stream.Send(msg)
	}

	// Periodic summary from server side too (useful for the dialer).
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendSummary()
			}
		}
	}()

	for {
		in, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if in == nil {
			continue
		}
		switch m := in.Msg.(type) {
		case *ledgerv1.GossipStreamRequest_Summary:
			_ = m
			// Respond with our summary immediately.
			sendSummary()
		default:
			// ignore for now
		}
		if peerLabel == "unknown-peer" {
			// We don't know the peer id here yet; once Phase 4 adds peer identity on streams,
			// we can attribute events more precisely.
			peerLabel = "peer"
		}
	}
}

func (n *Node) GetBlocks(req *ledgerv1.GetBlocksRequest, stream ledgerv1.NodeService_GetBlocksServer) error {
	if req == nil {
		return fmt.Errorf("nil request")
	}

	start := req.GetStartHeight()
	end := req.GetEndHeight()
	if end == 0 {
		tip, _ := n.store.Tip()
		end = tip
	}

	blocks, err := n.store.GetRange(start, end)
	if err != nil {
		return err
	}

	var sent uint64
	for i := range blocks {
		if err := stream.Send(&ledgerv1.GetBlocksResponse{Block: ledgerBlockToPB(blocks[i])}); err != nil {
			return err
		}
		sent++
	}

	if sent > 0 {
		peerAddr := "unknown"
		if p, ok := peer.FromContext(stream.Context()); ok && p.Addr != nil {
			peerAddr = p.Addr.String()
		}
		n.events.Publish(&ledgerv1.NodeEvent{
			At: timestamppb.Now(),
			Event: &ledgerv1.NodeEvent_BlocksSent{
				BlocksSent: &ledgerv1.BlocksSent{
					Peer:  &ledgerv1.Peer{NodeId: "unknown", Addr: peerAddr},
					Count: sent,
				},
			},
		})
	}
	return nil
}

// --- ObserverService ---

func (n *Node) GetNodeInfo(ctx context.Context, _ *ledgerv1.GetNodeInfoRequest) (*ledgerv1.GetNodeInfoResponse, error) {
	_ = ctx
	return &ledgerv1.GetNodeInfoResponse{
		ProtocolVersion: ProtocolVersion,
		SoftwareVersion: SoftwareVersion,
		NodeId:          n.id,
		ListenAddr:      n.listenAddr,
		Summary:         summaryFromStore(n.store),
		ConnectedPeers:  n.peers.ListPeersPB(),
	}, nil
}

func (n *Node) StreamNodeEvents(_ *ledgerv1.StreamNodeEventsRequest, stream ledgerv1.ObserverService_StreamNodeEventsServer) error {
	ch, cancel := n.events.Subscribe(128)
	defer cancel()

	// initial snapshot as a tip-change event (from zero values)
	h, hash := n.store.Tip()
	_ = stream.Send(&ledgerv1.StreamNodeEventsResponse{
		Event: &ledgerv1.NodeEvent{
			At: timestamppb.Now(),
			Event: &ledgerv1.NodeEvent_LedgerTipChanged{
				LedgerTipChanged: &ledgerv1.LedgerTipChanged{
					OldHeight: 0,
					OldHash:   make([]byte, 32),
					NewHeight: h,
					NewHash:   hash[:],
				},
			},
		},
	})

	// heartbeat to keep observers updated even when no events are occurring
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	heartbeat := func() error {
		return stream.Send(&ledgerv1.StreamNodeEventsResponse{
			Event: &ledgerv1.NodeEvent{At: timestamppb.Now()},
		})
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			if err := heartbeat(); err != nil {
				return err
			}
		case ev, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(&ledgerv1.StreamNodeEventsResponse{Event: ev}); err != nil {
				return err
			}
		}
	}
}

func (n *Node) Logf(format string, args ...any) {
	log.Printf("[node %s] %s", n.id, fmt.Sprintf(format, args...))
}

