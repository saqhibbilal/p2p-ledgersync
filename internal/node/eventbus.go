package node

import (
	"sync"
	"sync/atomic"

	ledgerv1 "ledger-sync/gen/go/ledger/v1"
)

// EventBus is a tiny in-process pubsub for observer streaming.
// It is best-effort: slow subscribers may drop events.
type EventBus struct {
	nextID atomic.Int64

	mu   sync.RWMutex
	subs map[int64]chan *ledgerv1.NodeEvent
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[int64]chan *ledgerv1.NodeEvent)}
}

func (b *EventBus) Publish(ev *ledgerv1.NodeEvent) {
	if ev == nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// drop
		}
	}
}

// Subscribe returns a channel receiving events and a cancel function.
func (b *EventBus) Subscribe(buffer int) (<-chan *ledgerv1.NodeEvent, func()) {
	if buffer <= 0 {
		buffer = 64
	}
	id := b.nextID.Add(1)
	ch := make(chan *ledgerv1.NodeEvent, buffer)

	b.mu.Lock()
	b.subs[id] = ch
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if c, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(c)
		}
		b.mu.Unlock()
	}

	return ch, cancel
}

