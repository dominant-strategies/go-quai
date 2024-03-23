// Copyright 2014 The go-ethereum Authors
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

package core

import (
	"errors"

	"github.com/dominant-strategies/go-quai/core/types"
)

var (
	// ErrKnownBlock is returned when a block to import is already known locally.
	ErrKnownBlock = errors.New("block already known")

	// ErrBannedHash is returned if a block to import is on the banned list.
	ErrBannedHash = errors.New("banned hash")

	// ErrNoGenesis is returned when there is no Genesis Block.
	ErrNoGenesis = errors.New("genesis not found in chain")

	// ErrSubNotSyncedToDom is returned when the subordinate cannot find the parent of the block which is being appended by the dom.
	ErrSubNotSyncedToDom = errors.New("sub not synced to dom")

	// ErrPendingEtxAlreadyKnown is returned received pending etx already in the cache/db
	ErrPendingEtxAlreadyKnown = errors.New("pending etx already known")

	// ErrBloomAlreadyKnown is returned if received bloom is already in the cache/db
	ErrBloomAlreadyKnown = errors.New("bloom already known")

	// ErrBodyNotFound is returned when body data for a given header hash cannot be found.
	ErrBodyNotFound = errors.New("could not find the body data to match the header root hash")

	// ErrDomClientNotUp is returned when block is trying to be appended when domClient is not up.
	ErrDomClientNotUp = errors.New("dom client is not online")

	// ErrBadSubManifest is returned when a block's subordinate manifest does not match the subordinate manifest hash
	ErrBadSubManifest = errors.New("subordinate manifest is incorrect")

	// ErrBadInterlink is returned when a block's interlink does not match the interlink hash
	ErrBadInterlink = errors.New("interlink is incorrect")

	//ErrPendingBlock indicates the block couldn't yet be processed. This is likely due to missing information (ancestor, body, pendingEtxs, etc)
	ErrPendingBlock = errors.New("block cannot be appended yet")

	//ErrPendingEtxNotFound is returned when pendingEtxs cannot be found for a hash given in the submanifest
	ErrPendingEtxNotFound = errors.New("pending etx not found")

	//ErrBloomNotFound is returned when bloom cannot be found for a hash
	ErrBloomNotFound = errors.New("bloom not found")

	//ErrPendingEtxRollupNotFound is returned when pendingEtxsRollup cannot be found for a hash given in the submanifest
	ErrPendingEtxRollupNotFound = errors.New("pending etx rollup not found")

	//ErrPendingEtxNotValid is returned when pendingEtxs is not valid
	ErrPendingEtxNotValid = errors.New("pending etx not valid")

	//ErrPendingEtxRollupNotValid is returned when pendingEtxsRollup is not valid
	ErrPendingEtxRollupNotValid = errors.New("pending etx rollup not valid")

	// ErrBadBlockHash is returned when block being appended is in the badBlockHashes list
	ErrBadBlockHash = errors.New("block hash exists in bad block hashes list")

	// ErrPendingHeaderNotInCache is returned when a coord gives an update but the slice has not yet created the referenced ph
	ErrPendingHeaderNotInCache = errors.New("no pending header found in cache")
)

// List of evm-call-message pre-checking errors. All state transition messages will
// be pre-checked before execution. If any invalidation detected, the corresponding
// error should be returned which is defined here.
//
// - If the pre-checking happens in the miner, then the transaction won't be packed.
// - If the pre-checking happens in the block processing procedure, then a "BAD BLOCk"
// error should be emitted.
var (
	// ErrNonceTooLow is returned if the nonce of a transaction is lower than the
	// one present in the local chain.
	ErrNonceTooLow = errors.New("nonce too low")

	// ErrNonceTooHigh is returned if the nonce of a transaction is higher than the
	// next one expected based on the local chain.
	ErrNonceTooHigh = errors.New("nonce too high")

	// ErrGasLimitReached is returned by the gas pool if the amount of gas required
	// by a transaction is higher than what's left in the block.
	ErrGasLimitReached = errors.New("gas limit reached")

	// ErrEtxLimitReached is returned when the ETXs emitted by a transaction
	// would violate the block's ETX limits.
	ErrEtxLimitReached = errors.New("etx limit reached")

	// ErrEtxGasLimitReached is returned when the gas limit of an ETX is greater
	// than the maximum allowed.
	ErrEtxGasLimitReached = errors.New("etx gas limit greater than maximum allowed")

	// ErrInsufficientFundsForTransfer is returned if the transaction sender doesn't
	// have enough funds for transfer(topmost call only).
	ErrInsufficientFundsForTransfer = errors.New("insufficient funds for transfer")

	// ErrInsufficientFunds is returned if the total cost of executing a transaction
	// is higher than the balance of the user's account.
	ErrInsufficientFunds = errors.New("insufficient funds for gas * price + value")

	// ErrGasUintOverflow is returned when calculating gas usage.
	ErrGasUintOverflow = errors.New("gas uint64 overflow")

	// ErrIntrinsicGas is returned if the transaction is specified to use less gas
	// than required to start the invocation.
	ErrIntrinsicGas = errors.New("intrinsic gas too low")

	// ErrTxTypeNotSupported is returned if a transaction is not supported in the
	// current network configuration.
	ErrTxTypeNotSupported = types.ErrTxTypeNotSupported

	// ErrTipAboveFeeCap is a sanity error to ensure no one is able to specify a
	// transaction with a tip higher than the total fee cap.
	ErrTipAboveFeeCap = errors.New("max priority fee per gas higher than max fee per gas")

	// ErrTipVeryHigh is a sanity error to avoid extremely big numbers specified
	// in the tip field.
	ErrTipVeryHigh = errors.New("max priority fee per gas higher than 2^256-1")

	// ErrFeeCapVeryHigh is a sanity error to avoid extremely big numbers specified
	// in the fee cap field.
	ErrFeeCapVeryHigh = errors.New("max fee per gas higher than 2^256-1")

	// ErrFeeCapTooLow is returned if the transaction fee cap is less than the
	// the base fee of the block.
	ErrFeeCapTooLow = errors.New("max fee per gas less than block base fee")

	// ErrSenderNoEOA is returned if the sender of a transaction is a contract.
	ErrSenderNoEOA = errors.New("sender not an eoa")

	// ErrSenderInoperable is returned if the sender of a transaction is outside of context.
	ErrSenderInoperable = errors.New("sender is in inoperable state")
)
