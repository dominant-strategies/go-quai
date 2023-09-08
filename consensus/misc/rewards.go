package misc

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/core/types"
)

// CalculateReward calculates the coinbase rewards depending on the type of the block
func CalculateReward(header *types.Header) *big.Int {
	//// This Reward Schedule is only for Iron Age Testnet and has nothing to do
	//// with the Mainnet Schedule
	return new(big.Int).Mul(header.Difficulty(), big.NewInt(10e8))
}
