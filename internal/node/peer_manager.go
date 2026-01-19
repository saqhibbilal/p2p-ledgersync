package node

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	ledgerv1 "ledger-sync/gen/go/ledger/v1"
	"ledger-sync/internal/ledger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PeerManager struct {
	n *Node

	mu    sync.RWMutex
	peers map[string]*PeerConn // key: addr
}

type PeerConn struct {
	addr    string
	nodeID  string
	conn    *grpc.ClientConn
	client  ledgerv1.NodeServiceClient
	closing chan struct{}

	mu            sync.Mutex
	remoteTip     uint64
	syncInFlight  bool
	lastHeartbeat time.Time
}

func NewPeerManager(n *Node) *PeerManager {
	return &PeerManager{
		n:     n,
		peers: make(map[string]*PeerConn),
	}
}

func (pm *PeerManager) ListPeersPB() []*ledgerv1.Peer {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]*ledgerv1.Peer, 0, len(pm.peers))
	for _, p := range pm.peers {
		out = append(out, &ledgerv1.Peer{NodeId: p.nodeID, Addr: p.addr})
	}
	return out
}

func parseSeeds(seedsCSV string) []string {
	if strings.TrimSpace(seedsCSV) == "" {
		return nil
	}
	parts := strings.Split(seedsCSV, ",")
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

func (pm *PeerManager) EnsurePeers(ctx context.Context, addrs []string) {
	for _, addr := range addrs {
		addr := addr
		go func() {
			if err := pm.EnsurePeer(ctx, addr); err != nil {
				pm.n.Logf("peer dial failed addr=%s err=%v", addr, err)
			}
		}()
	}
}

func (pm *PeerManager) EnsurePeer(ctx context.Context, addr string) error {
	if addr == "" {
		return errors.New("empty addr")
	}
	if addr == pm.n.ListenAddr() {
		return nil
	}

	pm.mu.RLock()
	if _, ok := pm.peers[addr]; ok {
		pm.mu.RUnlock()
		return nil
	}
	pm.mu.RUnlock()

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		dialCtx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return err
	}

	client := ledgerv1.NewNodeServiceClient(conn)

	hsCtx, hsCancel := context.WithTimeout(ctx, 5*time.Second)
	defer hsCancel()
	hs, err := client.Handshake(hsCtx, &ledgerv1.HandshakeRequest{
		ProtocolVersion: ProtocolVersion,
		SoftwareVersion: SoftwareVersion,
		NodeId:          pm.n.ID(),
		ListenAddr:      pm.n.ListenAddr(),
	})
	if err != nil {
		_ = conn.Close()
		return err
	}

	p := &PeerConn{
		addr:           addr,
		nodeID:         hs.GetNodeId(),
		conn:           conn,
		client:         client,
		closing:        make(chan struct{}),
		lastHeartbeat:  time.Now(),
		remoteTip:      0,
		syncInFlight:   false,
	}
	if hs.GetSummary() != nil {
		p.remoteTip = hs.GetSummary().GetTipHeight()
	}

	pm.mu.Lock()
	// Double-check in case another goroutine won the race.
	if _, ok := pm.peers[addr]; ok {
		pm.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	pm.peers[addr] = p
	pm.mu.Unlock()

	pm.n.Logf("peer up addr=%s node_id=%s tip=%d", addr, p.nodeID, p.remoteTip)
	pm.n.events.Publish(&ledgerv1.NodeEvent{
		At: timestamppb.Now(),
		Event: &ledgerv1.NodeEvent_PeerUp{
			PeerUp: &ledgerv1.PeerUp{Peer: &ledgerv1.Peer{NodeId: p.nodeID, Addr: p.addr}},
		},
	})

	go pm.runGossip(ctx, p)
	go pm.maybeSyncFromPeer(ctx, p, p.remoteTip)

	return nil
}

func (pm *PeerManager) runGossip(ctx context.Context, p *PeerConn) {
	defer func() {
		pm.mu.Lock()
		delete(pm.peers, p.addr)
		pm.mu.Unlock()
		_ = p.conn.Close()

		pm.n.Logf("peer down addr=%s node_id=%s", p.addr, p.nodeID)
		pm.n.events.Publish(&ledgerv1.NodeEvent{
			At: timestamppb.Now(),
			Event: &ledgerv1.NodeEvent_PeerDown{
				PeerDown: &ledgerv1.PeerDown{
					Peer:   &ledgerv1.Peer{NodeId: p.nodeID, Addr: p.addr},
					Reason: "disconnected",
				},
			},
		})
	}()

	stream, err := p.client.GossipStream(ctx)
	if err != nil {
		pm.n.Logf("gossip stream error addr=%s err=%v", p.addr, err)
		return
	}

	// send loop
	sendTicker := time.NewTicker(1 * time.Second)
	defer sendTicker.Stop()

	send := func() error {
		msg := &ledgerv1.GossipStreamRequest{
			SentAt: timestamppb.Now(),
			Msg: &ledgerv1.GossipStreamRequest_Summary{
				Summary: summaryFromStore(pm.n.store),
			},
		}
		return stream.Send(msg)
	}

	if err := send(); err != nil {
		pm.n.Logf("gossip send initial failed addr=%s err=%v", p.addr, err)
		return
	}

	// recv loop in its own goroutine
	recvErr := make(chan error, 1)
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			if resp == nil {
				continue
			}
			p.mu.Lock()
			p.lastHeartbeat = time.Now()
			p.mu.Unlock()

			switch m := resp.Msg.(type) {
			case *ledgerv1.GossipStreamResponse_Summary:
				if m.Summary == nil {
					continue
				}
				remoteTip := m.Summary.GetTipHeight()
				p.mu.Lock()
				p.remoteTip = remoteTip
				p.mu.Unlock()
				go pm.maybeSyncFromPeer(ctx, p, remoteTip)
			default:
				// ignore for now
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return
		case err := <-recvErr:
			if err != nil && !errors.Is(err, io.EOF) {
				pm.n.Logf("gossip recv ended addr=%s err=%v", p.addr, err)
			}
			return
		case <-sendTicker.C:
			if err := send(); err != nil {
				pm.n.Logf("gossip send failed addr=%s err=%v", p.addr, err)
				return
			}
		}
	}
}

func (pm *PeerManager) maybeSyncFromPeer(ctx context.Context, p *PeerConn, remoteTip uint64) {
	localTip, _ := pm.n.store.Tip()
	if remoteTip <= localTip {
		return
	}

	p.mu.Lock()
	if p.syncInFlight {
		p.mu.Unlock()
		return
	}
	p.syncInFlight = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.syncInFlight = false
		p.mu.Unlock()
	}()

	pm.n.Logf("sync start from=%s remoteTip=%d localTip=%d", p.addr, remoteTip, localTip)

	const maxBatch = uint64(256)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		localTip, _ = pm.n.store.Tip()
		p.mu.Lock()
		remoteTip = p.remoteTip
		p.mu.Unlock()

		if remoteTip <= localTip {
			pm.n.Logf("sync done from=%s localTip=%d", p.addr, localTip)
			return
		}

		start := localTip + 1
		end := remoteTip
		if end-start+1 > maxBatch {
			end = start + maxBatch - 1
		}

		stream, err := p.client.GetBlocks(ctx, &ledgerv1.GetBlocksRequest{
			StartHeight: start,
			EndHeight:   end,
		})
		if err != nil {
			pm.n.Logf("GetBlocks failed from=%s err=%v", p.addr, err)
			return
		}

		var blocks []ledger.Block
		var recv uint64
		for {
			resp, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				pm.n.Logf("GetBlocks recv failed from=%s err=%v", p.addr, err)
				break
			}
			if resp == nil || resp.Block == nil {
				continue
			}
			b, err := pbBlockToLedger(resp.Block)
			if err != nil {
				pm.n.Logf("block decode failed from=%s err=%v", p.addr, err)
				return
			}
			blocks = append(blocks, b)
			recv++
		}

		if len(blocks) == 0 {
			return
		}

		oldH, oldHash := pm.n.store.Tip()
		if err := pm.n.store.ApplyBlocks(blocks); err != nil {
			// If we raced and already have these heights, just stop; next gossip will reconcile.
			pm.n.Logf("ApplyBlocks failed from=%s err=%v", p.addr, err)
			return
		}
		newH, newHash := pm.n.store.Tip()

		pm.n.events.Publish(&ledgerv1.NodeEvent{
			At: timestamppb.Now(),
			Event: &ledgerv1.NodeEvent_BlocksReceived{
				BlocksReceived: &ledgerv1.BlocksReceived{
					Peer:  &ledgerv1.Peer{NodeId: p.nodeID, Addr: p.addr},
					Count: recv,
				},
			},
		})
		pm.n.events.Publish(&ledgerv1.NodeEvent{
			At: timestamppb.Now(),
			Event: &ledgerv1.NodeEvent_LedgerTipChanged{
				LedgerTipChanged: &ledgerv1.LedgerTipChanged{
					OldHeight: oldH,
					OldHash:   oldHash[:],
					NewHeight: newH,
					NewHash:   newHash[:],
				},
			},
		})
	}
}

func (pm *PeerManager) DebugString() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	parts := make([]string, 0, len(pm.peers))
	for _, p := range pm.peers {
		p.mu.Lock()
		rt := p.remoteTip
		p.mu.Unlock()
		parts = append(parts, fmt.Sprintf("%s(%s tip=%d)", p.addr, p.nodeID, rt))
	}
	return strings.Join(parts, ", ")
}

