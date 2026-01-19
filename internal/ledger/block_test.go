package ledger

import (
	"testing"
	"time"
)

func TestGenesisIsDeterministic(t *testing.T) {
	g1 := NewGenesisBlock()
	g2 := NewGenesisBlock()

	if g1.Height != 0 || g2.Height != 0 {
		t.Fatalf("expected genesis height 0, got %d and %d", g1.Height, g2.Height)
	}
	if g1.Hash != g2.Hash {
		t.Fatalf("expected deterministic genesis hash, got %x vs %x", g1.Hash, g2.Hash)
	}
	if err := g1.ValidateBasic(); err != nil {
		t.Fatalf("genesis ValidateBasic error: %v", err)
	}
}

func TestNextBlockHashStable(t *testing.T) {
	gen := NewGenesisBlock()
	ts := time.Unix(100, 123).UTC()
	payload := []byte("hello")

	b1 := NewNextBlock(gen, ts, payload)
	b2 := NewNextBlock(gen, ts, payload)

	if b1.Hash != b2.Hash {
		t.Fatalf("expected same hash for same inputs, got %x vs %x", b1.Hash, b2.Hash)
	}
	if err := b1.ValidateBasic(); err != nil {
		t.Fatalf("ValidateBasic error: %v", err)
	}
}

