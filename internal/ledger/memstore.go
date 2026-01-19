package ledger

import (
	"sync"
)

// Store is the minimal ledger storage interface needed for synchronization.
type Store interface {
	Tip() (height uint64, hash [32]byte)
	HasHeight(height uint64) bool
	Get(height uint64) (Block, bool)
	GetRange(startHeight, endHeight uint64) ([]Block, error) // inclusive
	ApplyBlocks(blocks []Block) error
}

// MemStore keeps a contiguous chain from genesis..tip.
// It is safe for concurrent use.
type MemStore struct {
	mu     sync.RWMutex
	blocks []Block // index == height
}

func NewMemStoreWithGenesis(genesis Block) (*MemStore, error) {
	if genesis.Height != 0 {
		return nil, ErrNonContiguous
	}
	if err := genesis.ValidateBasic(); err != nil {
		return nil, err
	}
	cp := genesis
	cp.Payload = cloneBytes(genesis.Payload)
	return &MemStore{blocks: []Block{cp}}, nil
}

func NewMemStore() *MemStore {
	s, _ := NewMemStoreWithGenesis(NewGenesisBlock())
	return s
}

func (s *MemStore) Tip() (height uint64, hash [32]byte) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tip := s.blocks[len(s.blocks)-1]
	return tip.Height, tip.Hash
}

func (s *MemStore) HasHeight(height uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return height < uint64(len(s.blocks))
}

func (s *MemStore) Get(height uint64) (Block, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if height >= uint64(len(s.blocks)) {
		return Block{}, false
	}
	b := s.blocks[height]
	b.Payload = cloneBytes(b.Payload)
	return b, true
}

func (s *MemStore) GetRange(startHeight, endHeight uint64) ([]Block, error) {
	if endHeight < startHeight {
		return nil, ErrRangeInvalid
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if startHeight >= uint64(len(s.blocks)) {
		return []Block{}, nil
	}
	if endHeight >= uint64(len(s.blocks)) {
		endHeight = uint64(len(s.blocks)) - 1
	}

	out := make([]Block, 0, endHeight-startHeight+1)
	for h := startHeight; h <= endHeight; h++ {
		b := s.blocks[h]
		b.Payload = cloneBytes(b.Payload)
		out = append(out, b)
	}
	return out, nil
}

func (s *MemStore) ApplyBlocks(blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tip := s.blocks[len(s.blocks)-1]
	nextHeight := tip.Height + 1

	for i := range blocks {
		b := blocks[i]

		if b.Height < nextHeight {
			return ErrHeightAlreadyHave
		}
		if b.Height != nextHeight {
			return ErrNonContiguous
		}
		if b.PrevHash != tip.Hash {
			return ErrPrevHashMismatch
		}
		if err := b.ValidateBasic(); err != nil {
			return err
		}

		cp := b
		cp.Payload = cloneBytes(b.Payload)
		s.blocks = append(s.blocks, cp)

		tip = cp
		nextHeight++
	}
	return nil
}

