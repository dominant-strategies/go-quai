package misc

import (
	"log"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
)

// CalculateReward calculates the coinbase rewards depending on the type of the block
// regions = # of regions
// zones = # of zones
// For each prime = Reward/3
// For each region = Reward/(3*regions*time-factor)
// For each zone = Reward/(3*regions*zones*time-factor^2)
func CalculateRewardAtContext(ctx int) *big.Int {
	reward := big.NewInt(5e18)
	timeFactor := big.NewInt(10)
	regions := big.NewInt(3)
	zones := big.NewInt(3)
	finalReward := new(big.Int)
	switch ctx {
	case common.PRIME_CTX:
		primeReward := big.NewInt(3)
		primeReward.Div(reward, primeReward)
		finalReward = primeReward
	case common.REGION_CTX:
		regionReward := big.NewInt(3)
		regionReward.Mul(regionReward, regions)
		regionReward.Mul(regionReward, timeFactor)
		regionReward.Div(reward, regionReward)
		finalReward = regionReward
	case common.ZONE_CTX:
		zoneReward := big.NewInt(3)
		zoneReward.Mul(zoneReward, regions)
		zoneReward.Mul(zoneReward, zones)
		zoneReward.Mul(zoneReward, timeFactor)
		zoneReward.Mul(zoneReward, timeFactor)
		zoneReward.Div(reward, zoneReward)
		finalReward = zoneReward
	default:
		log.Fatal("unknown node context")
		return nil
	}

	return finalReward
}
