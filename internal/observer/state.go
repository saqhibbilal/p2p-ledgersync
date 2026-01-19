package observer

import (
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusSynced  Status = "synced"
	StatusSyncing Status = "syncing"
	StatusOffline Status = "offline"
)

type NodeState struct {
	ID        string    `json:"id"`
	Addr      string    `json:"addr"`
	Protocol  uint32    `json:"protocol"`
	Software  string    `json:"software"`
	TipHeight uint64    `json:"tipHeight"`
	TipHash   string    `json:"tipHash"` // hex

	BlocksSent     uint64    `json:"blocksSent"`
	BlocksReceived uint64    `json:"blocksReceived"`
	LastSeen       time.Time `json:"lastSeen"`
	Online         bool      `json:"online"`
	Status         Status    `json:"status"`
	Lag            uint64    `json:"lag"`
}

type EdgeState struct {
	FromID   string    `json:"fromId"`
	FromAddr string    `json:"fromAddr"`
	ToID     string    `json:"toId"`
	ToAddr   string    `json:"toAddr"`
	Up       bool      `json:"up"`
	LastAt   time.Time `json:"lastAt"`
}

type LogEntry struct {
	At       time.Time `json:"at"`
	Type     string    `json:"type"` // peer | blocks | tip | info
	NodeID   string    `json:"nodeId"`
	NodeAddr string    `json:"nodeAddr"`
	PeerID   string    `json:"peerId,omitempty"`
	PeerAddr string    `json:"peerAddr,omitempty"`
	Message  string    `json:"message"`
}

type Snapshot struct {
	At           time.Time   `json:"at"`
	NetworkTip   uint64      `json:"networkTip"`
	OnlineCount  int         `json:"onlineCount"`
	TotalCount   int         `json:"totalCount"`
	SyncedCount  int         `json:"syncedCount"`
	SyncingCount int         `json:"syncingCount"`
	OfflineCount int         `json:"offlineCount"`
	Nodes        []NodeState `json:"nodes"`
	Edges        []EdgeState `json:"edges"`
	Events       []LogEntry  `json:"events"`
}

type Store struct {
	mu sync.RWMutex

	nodesByAddr map[string]*NodeState
	edges       map[string]*EdgeState // key = fromAddr + "->" + toAddr
	logs        []LogEntry
	logCap      int

	networkTip uint64
}

func NewStore() *Store {
	return &Store{
		nodesByAddr: make(map[string]*NodeState),
		edges:       make(map[string]*EdgeState),
		logCap:      400,
	}
}

func (s *Store) UpsertNode(addr, id string) *NodeState {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodesByAddr[addr]
	if !ok {
		n = &NodeState{Addr: addr}
		s.nodesByAddr[addr] = n
	}
	if id != "" {
		n.ID = id
	}
	return n
}

func (s *Store) UpdateNodeInfo(addr string, protocol uint32, software string, tipHeight uint64, tipHash []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodesByAddr[addr]
	if !ok {
		n = &NodeState{Addr: addr}
		s.nodesByAddr[addr] = n
	}
	// Avoid wiping known values when an event only updates tip.
	if protocol != 0 {
		n.Protocol = protocol
	}
	if software != "" {
		n.Software = software
	}
	n.TipHeight = tipHeight
	if len(tipHash) > 0 {
		n.TipHash = hex.EncodeToString(tipHash)
	}
	n.LastSeen = time.Now()
	n.Online = true
}

func (s *Store) AddLog(e LogEntry) {
	if e.Message == "" {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, e)
	if s.logCap <= 0 {
		s.logCap = 400
	}
	if len(s.logs) > s.logCap {
		// drop oldest
		s.logs = s.logs[len(s.logs)-s.logCap:]
	}
}

func (s *Store) MarkSeen(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodesByAddr[addr]; ok {
		n.LastSeen = time.Now()
		n.Online = true
	}
}

func (s *Store) MarkOffline(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodesByAddr[addr]; ok {
		n.Online = false
	}
}

func (s *Store) AddBlocksReceived(addr string, count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodesByAddr[addr]; ok {
		n.BlocksReceived += count
	}
}

func (s *Store) AddBlocksSent(addr string, count uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.nodesByAddr[addr]; ok {
		n.BlocksSent += count
	}
}

func edgeKey(fromAddr, toAddr string) string { return fromAddr + "->" + toAddr }

func (s *Store) SetEdge(fromAddr, fromID, toAddr, toID string, up bool) {
	if strings.TrimSpace(fromAddr) == "" || strings.TrimSpace(toAddr) == "" {
		return
	}
	k := edgeKey(fromAddr, toAddr)
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.edges[k]
	if !ok {
		e = &EdgeState{FromAddr: fromAddr, ToAddr: toAddr}
		s.edges[k] = e
	}
	if fromID != "" {
		e.FromID = fromID
	}
	if toID != "" {
		e.ToID = toID
	}
	e.Up = up
	e.LastAt = time.Now()
}

func (s *Store) Snapshot(offlineAfter time.Duration) Snapshot {
	now := time.Now()

	s.mu.Lock()
	// Update online/offline based on staleness.
	for _, n := range s.nodesByAddr {
		if n.Online && offlineAfter > 0 && now.Sub(n.LastSeen) > offlineAfter {
			n.Online = false
		}
	}

	// Compute network tip from online nodes.
	var networkTip uint64
	for _, n := range s.nodesByAddr {
		if n.Online && n.TipHeight > networkTip {
			networkTip = n.TipHeight
		}
	}
	s.networkTip = networkTip

	nodes := make([]NodeState, 0, len(s.nodesByAddr))
	for _, n := range s.nodesByAddr {
		cp := *n
		if !cp.Online {
			cp.Status = StatusOffline
			cp.Lag = 0
		} else if networkTip > cp.TipHeight {
			cp.Status = StatusSyncing
			cp.Lag = networkTip - cp.TipHeight
		} else {
			cp.Status = StatusSynced
			cp.Lag = 0
		}
		nodes = append(nodes, cp)
	}

	edges := make([]EdgeState, 0, len(s.edges))
	for _, e := range s.edges {
		edges = append(edges, *e)
	}

	// Newest-first, capped for UI payload size.
	const maxEvents = 200
	events := make([]LogEntry, 0, minInt(maxEvents, len(s.logs)))
	for i := len(s.logs) - 1; i >= 0 && len(events) < maxEvents; i-- {
		events = append(events, s.logs[i])
	}
	s.mu.Unlock()

	sort.Slice(nodes, func(i, j int) bool {
		ai := nodes[i].ID
		aj := nodes[j].ID
		if ai == "" {
			ai = nodes[i].Addr
		}
		if aj == "" {
			aj = nodes[j].Addr
		}
		return ai < aj
	})

	total := len(nodes)
	online := 0
	synced := 0
	syncing := 0
	offline := 0
	for i := range nodes {
		if nodes[i].Status == StatusOffline {
			offline++
			continue
		}
		online++
		if nodes[i].Status == StatusSynced {
			synced++
		} else {
			syncing++
		}
	}

	return Snapshot{
		At:           now,
		NetworkTip:   networkTip,
		OnlineCount:  online,
		TotalCount:   total,
		SyncedCount:  synced,
		SyncingCount: syncing,
		OfflineCount: offline,
		Nodes:        nodes,
		Edges:        edges,
		Events:       events,
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

