package ledger

import (
	"testing"
	"time"
)

func TestMemStoreAppliesContiguousBlocks(t *testing.T) {
	s := NewMemStore()

	_, tipHash := s.Tip()
	gen, ok := s.Get(0)
	if !ok {
		t.Fatalf("expected genesis present")
	}
	if tipHash != gen.Hash {
		t.Fatalf("expected tip hash to equal genesis hash")
	}

	now := time.Unix(200, 0).UTC()
	blocks := make([]Block, 0, 5)
	prev := gen
	for i := 0; i < 5; i++ {
		b := NewNextBlock(prev, now.Add(time.Duration(i)*time.Second), []byte{byte(i)})
		blocks = append(blocks, b)
		prev = b
	}

	if err := s.ApplyBlocks(blocks); err != nil {
		t.Fatalf("ApplyBlocks error: %v", err)
	}

	h, _ := s.Tip()
	if h != 5 {
		t.Fatalf("expected tip height 5, got %d", h)
	}
	if !s.HasHeight(5) || s.HasHeight(6) {
		t.Fatalf("HasHeight unexpected result")
	}

	got, err := s.GetRange(3, 5)
	if err != nil {
		t.Fatalf("GetRange error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(got))
	}
	if got[0].Height != 3 || got[2].Height != 5 {
		t.Fatalf("unexpected heights in range: %d..%d", got[0].Height, got[len(got)-1].Height)
	}
}

func TestMemStoreRejectsGap(t *testing.T) {
	s := NewMemStore()
	gen, _ := s.Get(0)

	b1 := NewNextBlock(gen, time.Unix(10, 0).UTC(), []byte("a"))
	b2 := b1
	b2.Height = b1.Height + 2 // gap
	b2.Hash = b2.ComputeHash()

	err := s.ApplyBlocks([]Block{b1, b2})
	if err != ErrNonContiguous {
		t.Fatalf("expected ErrNonContiguous, got %v", err)
	}
}

func TestMemStoreRejectsPrevHashMismatch(t *testing.T) {
	s := NewMemStore()
	gen, _ := s.Get(0)

	b1 := NewNextBlock(gen, time.Unix(10, 0).UTC(), []byte("a"))
	b1.PrevHash = [32]byte{1, 2, 3} // corrupt link
	b1.Hash = b1.ComputeHash()

	err := s.ApplyBlocks([]Block{b1})
	if err != ErrPrevHashMismatch {
		t.Fatalf("expected ErrPrevHashMismatch, got %v", err)
	}
}

