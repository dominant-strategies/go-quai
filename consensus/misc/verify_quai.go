// Copyright 2021 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package misc

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/spruce-solutions/go-quai/common/math"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/params"
)

// VerifyHeaderGasAndFee verifies some header attributes which were changed in EIP-1559,
// - gas limit check
// - basefee check
func VerifyHeaderGasAndFee(config *params.ChainConfig, parent, header *types.Header, chain consensus.ChainHeaderReader) error {
	// Verify that the gas limit remains within allowed bounds
	parentGasLimit := parent.GasLimit[types.QuaiNetworkContext]
	if !config.IsLondon(parent.Number[types.QuaiNetworkContext]) {
		parentGasLimit = parent.GasLimit[types.QuaiNetworkContext] * params.ElasticityMultiplier
	}

	if err := VerifyGaslimit(parentGasLimit, header.GasLimit[types.QuaiNetworkContext]); err != nil {
		return err
	}

	// Verify the header is not malformed
	if header.BaseFee == nil {
		return fmt.Errorf("header is missing baseFee")
	}
	// Verify the baseFee is correct based on the parent header.
	expectedBaseFee := CalcBaseFee(config, parent, chain.GetHeaderByNumber, chain.GetUnclesInChain, chain.GetGasUsedInChain)
	if header.BaseFee[types.QuaiNetworkContext].Cmp(expectedBaseFee) != 0 {
		return fmt.Errorf("invalid baseFee: have %s, want %s, parentBaseFee %s, parentGasUsed %d",
			expectedBaseFee, header.BaseFee, parent.BaseFee, parent.GasUsed)
	}
	return nil
}

// CalcBaseFee calculates the basefee of the header.
func CalcBaseFee(config *params.ChainConfig, parent *types.Header, headerByNumber func(number uint64) *types.Header, getUncles func(block *types.Block, length int) []*types.Header, getGasUsed func(block *types.Block, length int) int64) *big.Int {
	// If the chain is not beyond 1000 blocks, return the initial basefee.
	if parent.Number[types.QuaiNetworkContext].Int64() < 1000 {
		return new(big.Int).SetUint64(params.InitialBaseFee)
	}

	var (
		slopeLength        = 500
		slopeLengthDivisor = big.NewInt(int64(slopeLength))
		reward             = CalculateReward()
	)

	// Transform the parent header into a block.
	parentBlock := types.NewBlockWithHeader(parent)

	header500 := headerByNumber(uint64(parent.Number[types.QuaiNetworkContext].Int64() - 500))
	if header500 == nil {
		return new(big.Int).SetUint64(params.InitialBaseFee)
	}

	// Get the 500th previous block in order to calculate slope on the uncle rate and gas used.
	block500 := types.NewBlockWithHeader(header500)

	// Get applicable uncle count and gas used across two various slope points
	uncleCount1 := big.NewInt(int64(len(getUncles(parentBlock, slopeLength))))
	uncleCount2 := big.NewInt(int64(len(getUncles(block500, slopeLength))))
	gasUsed1 := big.NewInt(getGasUsed(parentBlock, slopeLength))
	gasUsed2 := big.NewInt(getGasUsed(block500, slopeLength))

	// Calculate uncle rate based on slope length converted to big.Int
	uncleRate1 := uncleCount1.Div(uncleCount1, slopeLengthDivisor)
	uncleRate2 := uncleCount2.Div(uncleCount2, slopeLengthDivisor)

	// Generate numerator and denominator to calculate base fee with given bounds.
	numerator := math.BigMax(uncleCount1.Sub(uncleRate1, uncleRate2), big.NewInt(0))
	denominator := math.BigMax(gasUsed1.Sub(gasUsed1, gasUsed2), big.NewInt(1000))

	result := big.NewInt(0)
	result.Div(numerator, denominator).Mul(result, reward)
	if result.Cmp(big.NewInt(params.InitialBaseFee)) < 0 {
		return big.NewInt(params.InitialBaseFee)
	} else {
		return result
	}
}

// CalculateReward calculates the coinbase rewards depending on the type of the block
// regions = # of regions
// zones = # of zones
// For each prime = Reward/3
// For each region = Reward/(3*regions*time-factor)
// For each zone = Reward/(3*regions*zones*time-factor^2)
func CalculateReward() *big.Int {

	reward := big.NewInt(5e18)

	timeFactor := big.NewInt(10)

	regions := big.NewInt(3)
	zones := big.NewInt(3)

	finalReward := new(big.Int)

	if types.QuaiNetworkContext == 0 {
		primeReward := big.NewInt(3)
		primeReward.Div(reward, primeReward)
		finalReward = primeReward
	}
	if types.QuaiNetworkContext == 1 {
		regionReward := big.NewInt(3)
		regionReward.Mul(regionReward, regions)
		regionReward.Mul(regionReward, timeFactor)
		regionReward.Div(reward, regionReward)
		finalReward = regionReward
	}
	if types.QuaiNetworkContext == 2 {
		zoneReward := big.NewInt(3)
		zoneReward.Mul(zoneReward, regions)
		zoneReward.Mul(zoneReward, zones)
		zoneReward.Mul(zoneReward, timeFactor)
		zoneReward.Mul(zoneReward, timeFactor)
		zoneReward.Div(reward, zoneReward)
		finalReward = zoneReward
	}

	return finalReward
}

// blockOntology is used to retrieve the MapContext of a given block.
func BlockOntology(number []*big.Int) ([]int, error) {
	forkNumber := number[0]

	switch {
	case forkNumber.Cmp(params.FullerMapContext) >= 0:
		return params.FullerOntology, nil
	default:
		return nil, errors.New("invalid number passed to BlockOntology")
	}
}
