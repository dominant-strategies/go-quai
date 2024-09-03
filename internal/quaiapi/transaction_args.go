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

package quaiapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/common/math"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/rpc"
)

// TransactionArgs represents the arguments to construct a new transaction
// or a message call.
type TransactionArgs struct {
	From     *common.Address `json:"from"`
	To       *common.Address `json:"to"`
	Gas      *hexutil.Uint64 `json:"gas"`
	GasPrice *hexutil.Big    `json:"gasPrice"`
	MinerTip *hexutil.Big    `json:"minerTip"`
	Value    *hexutil.Big    `json:"value"`
	Nonce    *hexutil.Uint64 `json:"nonce"`

	// We accept "data" and "input" for backwards-compatibility reasons.
	// "input" is the newer name and should be preferred by clients.
	Data  *hexutil.Bytes `json:"data"`
	Input *hexutil.Bytes `json:"input"`

	// Introduced by AccessListTxType transaction.
	AccessList *types.AccessList `json:"accessList,omitempty"`
	ChainID    *hexutil.Big      `json:"chainId,omitempty"`

	// Support for Qi (UTXO) transaction
	TxIn   types.TxIns  `json:"txIn,omitempty"`
	TxOut  types.TxOuts `json:"txOut,omitempty"`
	TxType uint8        `json:"txType,omitempty"`
}

// from retrieves the transaction sender address.
func (arg *TransactionArgs) from(nodeLocation common.Location) common.Address {
	if arg.From == nil || arg.From.Equal(common.Zero) {
		return common.ZeroAddress(nodeLocation)
	}
	return *arg.From
}

// data retrieves the transaction calldata. Input field is preferred.
func (arg *TransactionArgs) data() []byte {
	if arg.Input != nil {
		return *arg.Input
	}
	if arg.Data != nil {
		return *arg.Data
	}
	return nil
}

// setDefaults fills in default values for unspecified tx fields.
func (args *TransactionArgs) setDefaults(ctx context.Context, b Backend) error {

	head := b.CurrentHeader()
	if args.MinerTip == nil {
		tip := big.NewInt(0)
		args.MinerTip = (*hexutil.Big)(tip)
	}
	if args.GasPrice == nil {
		gasFeeCap := new(big.Int).Add(
			(*big.Int)(args.MinerTip),
			new(big.Int).Mul(head.BaseFee(), big.NewInt(2)),
		)
		args.GasPrice = (*hexutil.Big)(gasFeeCap)
	}
	if args.GasPrice.ToInt().Cmp(args.MinerTip.ToInt()) < 0 {
		return fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.GasPrice, args.MinerTip)
	}
	if args.Value == nil {
		args.Value = new(hexutil.Big)
	}
	if args.Nonce == nil {
		nonce, err := b.GetPoolNonce(ctx, args.from(b.NodeLocation()))
		if err != nil {
			return err
		}
		args.Nonce = (*hexutil.Uint64)(&nonce)
	}
	if args.Data != nil && args.Input != nil && !bytes.Equal(*args.Data, *args.Input) {
		return errors.New(`both "data" and "input" are set and not equal. Please use "input" to pass transaction call data`)
	}
	if args.To == nil && len(args.data()) == 0 {
		return errors.New(`contract creation without any data provided`)
	}
	// Estimate the gas usage if necessary.
	if args.Gas == nil {
		// These fields are immutable during the estimation, safe to
		// pass the pointer directly.
		callArgs := TransactionArgs{
			From:       args.From,
			To:         args.To,
			GasPrice:   args.GasPrice,
			MinerTip:   args.MinerTip,
			Value:      args.Value,
			Data:       args.Data,
			AccessList: args.AccessList,
		}
		pendingBlockNr := rpc.BlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
		estimated, err := DoEstimateGas(ctx, b, callArgs, pendingBlockNr, b.RPCGasCap())
		if err != nil {
			return err
		}
		args.Gas = &estimated
		log.Global.WithField("gas", args.Gas).Debug("Estimate gas usage automatically")
	}
	if args.ChainID == nil {
		id := (*hexutil.Big)(b.ChainConfig().ChainID)
		args.ChainID = id
	}
	return nil
}

// ToMessage converts th transaction arguments to the Message type used by the
// core evm. This method is used in calls and traces that do not require a real
// live transaction.
func (args *TransactionArgs) ToMessage(globalGasCap uint64, baseFee *big.Int, nodeLocation common.Location) (types.Message, error) {
	if nodeLocation.Context() != common.ZONE_CTX {
		return types.Message{}, errors.New("toMessage can only called in zone chain")
	}
	// Set sender address or use zero address if none specified.
	addr := args.from(nodeLocation)

	// Set default gas & gas price if none were set
	gas := globalGasCap
	if gas == 0 {
		gas = uint64(math.MaxUint64 / 2)
	}
	if args.Gas != nil {
		gas = uint64(*args.Gas)
	}
	if globalGasCap != 0 && globalGasCap < gas {
		log.Global.WithFields(log.Fields{
			"requested": gas,
			"cap":       globalGasCap,
		}).Warn("Caller gas above allowance, capping")
		gas = globalGasCap
	}
	var (
		gasPrice *big.Int
		minerTip *big.Int
	)
	// User specified max fee (or none), use those
	gasPrice = new(big.Int)
	if args.GasPrice != nil {
		gasPrice = args.GasPrice.ToInt()
	}
	minerTip = gasPrice
	value := new(big.Int)
	if args.Value != nil {
		value = args.Value.ToInt()
	}
	data := args.data()
	var accessList types.AccessList
	if args.AccessList != nil {
		accessList = *args.AccessList
	}
	msg := types.NewMessage(addr, args.To, uint64(*args.Nonce), value, gas, gasPrice, minerTip, data, accessList, false)
	return msg, nil
}

// CalculateQiTxGas calculates the gas usage of a Qi transaction.
func (args *TransactionArgs) CalculateQiTxGas(qiScalingFactor float64, location common.Location) (hexutil.Uint64, error) {
	if args.TxType != types.QiTxType {
		return 0, errors.New("not a Qi transaction")
	}

	if len(args.TxIn) == 0 || len(args.TxOut) == 0 {
		return 0, errors.New("Qi transaction must have at least one input and one output")
	}

	qiTx := &types.QiTx{
		TxIn:  args.TxIn,
		TxOut: args.TxOut,
	}

	tx := types.NewTx(qiTx)
	return hexutil.Uint64(types.CalculateQiTxGas(tx, qiScalingFactor, location)), nil
}
