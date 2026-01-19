package ledger

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

// Block is a minimal chain-linked ledger entry.
//
// This project focuses on synchronization, so validation is intentionally light:
// we enforce continuity (height increments, prev-hash links) and deterministic hashing.
type Block struct {
	Height   uint64
	PrevHash [32]byte
	Hash     [32]byte
	Time     time.Time
	Payload  []byte
}

// NewGenesisBlock returns a deterministic genesis block.
func NewGenesisBlock() Block {
	b := Block{
		Height:   0,
		PrevHash: [32]byte{},
		Time:     time.Unix(0, 0).UTC(),
		Payload:  []byte("genesis"),
	}
	b.Hash = b.ComputeHash()
	return b
}

// NewNextBlock creates the next block after prev, with the provided timestamp and payload.
func NewNextBlock(prev Block, ts time.Time, payload []byte) Block {
	b := Block{
		Height:   prev.Height + 1,
		PrevHash: prev.Hash,
		Time:     ts.UTC(),
		Payload:  cloneBytes(payload),
	}
	b.Hash = b.ComputeHash()
	return b
}

func (b Block) ComputeHash() [32]byte {
	h := sha256.New()

	var buf8 [8]byte

	binary.LittleEndian.PutUint64(buf8[:], b.Height)
	_, _ = h.Write(buf8[:])

	_, _ = h.Write(b.PrevHash[:])

	binary.LittleEndian.PutUint64(buf8[:], uint64(b.Time.UTC().UnixNano()))
	_, _ = h.Write(buf8[:])

	_, _ = h.Write(b.Payload)

	var out [32]byte
	sum := h.Sum(nil)
	copy(out[:], sum)
	return out
}

// ValidateBasic verifies the block's self-hash matches its contents.
func (b Block) ValidateBasic() error {
	if b.Time.IsZero() {
		return errors.New("block time is zero")
	}
	if got := b.ComputeHash(); got != b.Hash {
		return ErrHashMismatch
	}
	return nil
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

