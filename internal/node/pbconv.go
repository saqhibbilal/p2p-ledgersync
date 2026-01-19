package node

import (
	"errors"
	"time"

	ledgerv1 "ledger-sync/gen/go/ledger/v1"
	"ledger-sync/internal/ledger"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func summaryFromStore(s ledger.Store) *ledgerv1.LedgerSummary {
	h, hash := s.Tip()
	return &ledgerv1.LedgerSummary{
		TipHeight: h,
		TipHash:   hash[:],
	}
}

func ledgerBlockToPB(b ledger.Block) *ledgerv1.Block {
	prev := b.PrevHash
	hash := b.Hash
	return &ledgerv1.Block{
		Height:   b.Height,
		PrevHash: prev[:],
		Hash:     hash[:],
		Time:     timestamppb.New(b.Time),
		Payload:  append([]byte(nil), b.Payload...),
	}
}

func pbBlockToLedger(b *ledgerv1.Block) (ledger.Block, error) {
	if b == nil {
		return ledger.Block{}, errors.New("nil block")
	}
	if len(b.PrevHash) != 32 {
		return ledger.Block{}, errors.New("prev_hash must be 32 bytes")
	}
	if len(b.Hash) != 32 {
		return ledger.Block{}, errors.New("hash must be 32 bytes")
	}
	var prev [32]byte
	var hash [32]byte
	copy(prev[:], b.PrevHash)
	copy(hash[:], b.Hash)

	var ts time.Time
	if b.Time != nil {
		ts = b.Time.AsTime()
	}
	out := ledger.Block{
		Height:   b.Height,
		PrevHash: prev,
		Hash:     hash,
		Time:     ts,
		Payload:  append([]byte(nil), b.Payload...),
	}
	if err := out.ValidateBasic(); err != nil {
		return ledger.Block{}, err
	}
	return out, nil
}

