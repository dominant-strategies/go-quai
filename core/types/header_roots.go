package types

import "github.com/spruce-solutions/go-quai/common"

type HeaderRoots struct {
	StateRoot    common.Hash
	TxsRoot      common.Hash
	ReceiptsRoot common.Hash
}
