// Copyright 2016 The go-ethereum Authors
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

package vm

import (
	"math/big"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

// StateDB is an EVM database for full state querying.
type StateDB interface {
	CreateAccount(common.InternalAddress)

	SubBalance(common.InternalAddress, *big.Int)
	AddBalance(common.InternalAddress, *big.Int)
	GetBalance(common.InternalAddress) *big.Int
	GetSize(common.InternalAddress) *big.Int

	GetNonce(common.InternalAddress) uint64
	SetNonce(common.InternalAddress, uint64)

	GetCodeHash(common.InternalAddress) common.Hash
	GetCode(common.InternalAddress) []byte
	SetCode(common.InternalAddress, []byte)
	GetCodeSize(common.InternalAddress) int

	AddRefund(uint64)
	SubRefund(uint64)
	GetRefund() uint64

	GetCommittedState(common.InternalAddress, common.Hash) common.Hash
	GetState(common.InternalAddress, common.Hash) common.Hash
	SetState(common.InternalAddress, common.Hash, common.Hash)

	Suicide(common.InternalAddress) bool
	HasSuicided(common.InternalAddress) bool

	// Exist reports whether the given account exists in state.
	// Notably this should also return true for suicided accounts.
	Exist(common.InternalAddress) bool
	// Empty returns whether the given account is empty. Empty
	// is defined according to (balance = nonce = code = 0).
	Empty(common.InternalAddress) bool

	PrepareAccessList(sender common.Address, dest *common.Address, precompiles []common.Address, txAccesses types.AccessList, debug bool)
	AddressInAccessList(addr common.AddressBytes) bool
	SlotInAccessList(addr common.AddressBytes, slot common.Hash) (addressOk bool, slotOk bool)
	// AddAddressToAccessList adds the given address to the access list. This operation is safe to perform
	// even if the feature/fork is not active yet
	AddAddressToAccessList(addr common.AddressBytes)
	// AddSlotToAccessList adds the given (address,slot) to the access list. This operation is safe to perform
	// even if the feature/fork is not active yet
	AddSlotToAccessList(addr common.AddressBytes, slot common.Hash)

	RevertToSnapshot(int)
	Snapshot() int

	AddLog(*types.Log)
	AddPreimage(common.Hash, []byte)

	ForEachStorage(common.InternalAddress, func(common.Hash, common.Hash) bool) error
}

// CallContext provides a basic interface for the EVM calling conventions. The EVM
// depends on this context being implemented for doing subcalls and initialising new EVM contracts.
type CallContext interface {
	// Call another contract
	Call(env *EVM, me ContractRef, addr common.InternalAddress, data []byte, gas, value *big.Int) ([]byte, error)
	// Take another's contract code and execute within our own context
	CallCode(env *EVM, me ContractRef, addr common.InternalAddress, data []byte, gas, value *big.Int) ([]byte, error)
	// Same as CallCode except sender and value is propagated from parent to child scope
	DelegateCall(env *EVM, me ContractRef, addr common.InternalAddress, data []byte, gas *big.Int) ([]byte, error)
	// Create a new contract
	Create(env *EVM, me ContractRef, data []byte, gas, value *big.Int) ([]byte, common.InternalAddress, error)
}
