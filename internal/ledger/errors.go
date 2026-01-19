package ledger

import "errors"

var (
	ErrHashMismatch      = errors.New("block hash does not match computed hash")
	ErrNonContiguous     = errors.New("blocks are not contiguous")
	ErrPrevHashMismatch  = errors.New("prev hash does not match current tip")
	ErrHeightAlreadyHave = errors.New("block height already present")
	ErrRangeInvalid      = errors.New("invalid range")
)

