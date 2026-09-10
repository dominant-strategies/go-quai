package core

import (
	"context"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/nipopow"
)

// GetNiPoPoWProof builds a read-only Prime NiPoPoW proof for RPC callers.
func (c *Core) GetNiPoPoWProof(ctx context.Context, anchor common.Hash, tip common.Hash, m uint64) (*nipopow.Proof, error) {
	return c.sl.hc.BuildPrimeNiPoPoWProof(ctx, anchor, tip, m)
}
