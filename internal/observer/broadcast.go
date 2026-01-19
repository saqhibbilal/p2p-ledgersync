package observer

import (
	"sync"
)

// Broadcaster provides a simple fan-out for snapshots.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[chan Snapshot]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[chan Snapshot]struct{})}
}

func (b *Broadcaster) Subscribe(buffer int) (<-chan Snapshot, func()) {
	if buffer <= 0 {
		buffer = 8
	}
	ch := make(chan Snapshot, buffer)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, cancel
}

func (b *Broadcaster) Publish(s Snapshot) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- s:
		default:
			// drop
		}
	}
}

