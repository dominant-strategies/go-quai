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
	From *common.Address `json:"from"`
	To   *common.Address `json:"to"`
	Gas  *hexutil.Uint64 `json:"gas"`
	// GasPrice             *hexutil.Big    `json:"gasPrice"` //581
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas"`
	Value                *hexutil.Big    `json:"value"`
	Nonce                *hexutil.Uint64 `json:"nonce"`

	// We accept "data" and "input" for backwards-compatibility reasons.
	// "input" is the newer name and should be preferred by clients.
	Data  *hexutil.Bytes `json:"data"`
	Input *hexutil.Bytes `json:"input"`

	// Introduced by AccessListTxType transaction.
	AccessList *types.AccessList `json:"accessList,omitempty"`
	ChainID    *hexutil.Big      `json:"chainId,omitempty"`
}

// from retrieves the transaction sender address.
func (arg *TransactionArgs) from() common.Address {
	if arg.From == nil {
		return common.ZeroAddr
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
	// if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
	if args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil { //modified 581
		return errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}
	head := b.CurrentHeader()
	// If user specifies both maxPriorityfee and maxFee, then we do not
	// need to consult the chain for defaults.
	if args.MaxPriorityFeePerGas == nil || args.MaxFeePerGas == nil {
		// In this clause, user left some fields unspecified.

		if args.MaxPriorityFeePerGas == nil {
			tip, err := b.SuggestGasTipCap(ctx)
			if err != nil {
				return err
			}
			args.MaxPriorityFeePerGas = (*hexutil.Big)(tip)
		}
		if args.MaxFeePerGas == nil {
			gasFeeCap := new(big.Int).Add(
				(*big.Int)(args.MaxPriorityFeePerGas),
				new(big.Int).Mul(head.BaseFee(), big.NewInt(2)),
			)
			args.MaxFeePerGas = (*hexutil.Big)(gasFeeCap)
		}
		if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
			return fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
		}

		price.Add(price, head.BaseFee())

	} else {
		// Both maxPriorityfee and maxFee set by caller. Sanity-check their internal relation
		if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
			return fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
		}
	}
	if args.Value == nil {
		args.Value = new(hexutil.Big)
	}
	if args.Nonce == nil {
		nonce, err := b.GetPoolNonce(ctx, args.from())
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
			From: args.From,
			To:   args.To,
			// GasPrice:             args.GasPrice, //remove 581
			MaxFeePerGas:         args.MaxFeePerGas,
			MaxPriorityFeePerGas: args.MaxPriorityFeePerGas,
			Value:                args.Value,
			Data:                 args.Data,
			AccessList:           args.AccessList,
		}
		pendingBlockNr := rpc.BlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
		estimated, err := DoEstimateGas(ctx, b, callArgs, pendingBlockNr, b.RPCGasCap())
		if err != nil {
			return err
		}
		args.Gas = &estimated
		log.Trace("Estimate gas usage automatically", "gas", args.Gas)
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
func (args *TransactionArgs) ToMessage(globalGasCap uint64, baseFee *big.Int) (types.Message, error) {
	nodeCtx := common.NodeLocation.Context()
	if nodeCtx != common.ZONE_CTX {
		return types.Message{}, errors.New("toMessage can only called in zone chain")
	}
	// Reject invalid combinations of fee styles
	if args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil {
		return types.Message{}, errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}
	// Set sender address or use zero address if none specified.
	addr := args.from()

	// Set default gas & gas price if none were set
	gas := globalGasCap
	if gas == 0 {
		gas = uint64(math.MaxUint64 / 2)
	}
	if args.Gas != nil {
		gas = uint64(*args.Gas)
	}
	if globalGasCap != 0 && globalGasCap < gas {
		log.Warn("Caller gas above allowance, capping", "requested", gas, "cap", globalGasCap)
		gas = globalGasCap
	}
	var (
		gasPrice  *big.Int
		gasFeeCap *big.Int
		gasTipCap *big.Int
	)
	if baseFee == nil {
		gasPrice = new(big.Int).SetUint64(0)
		gasFeeCap, gasTipCap = gasPrice, gasPrice
	} else {
		// A basefee is provided

		// User specified the legacy gas field, convert

		gasPrice = new(big.Int).SetUint64(0)
		gasFeeCap, gasTipCap = gasPrice, gasPrice
		// User specified max fee (or none), use those
		gasFeeCap = new(big.Int)
		if args.MaxFeePerGas != nil {
			gasFeeCap = args.MaxFeePerGas.ToInt()
		}
		gasTipCap = new(big.Int)
		if args.MaxPriorityFeePerGas != nil {
			gasTipCap = args.MaxPriorityFeePerGas.ToInt()
		}
		// Backfill the legacy gasPrice for EVM execution, unless we're all zeroes
		gasPrice = new(big.Int)
		if gasFeeCap.BitLen() > 0 || gasTipCap.BitLen() > 0 {
			gasPrice = math.BigMin(new(big.Int).Add(gasTipCap, baseFee), gasFeeCap)
		}

	}
	value := new(big.Int)
	if args.Value != nil {
		value = args.Value.ToInt()
	}
	data := args.data()
	var accessList types.AccessList
	if args.AccessList != nil {
		accessList = *args.AccessList
	}
	msg := types.NewMessage(addr, args.To, 0, value, gas, gasPrice, gasFeeCap, gasTipCap, data, accessList, false)
	return msg, nil
}
