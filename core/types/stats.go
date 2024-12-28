package types

import "math/big"

type BlockStats struct {
	QiConverted          *big.Int
	QuaiConverted        *big.Int
	Beta                 *big.Int
	Slip                 uint64
	KQuaiSlip            uint64
	ConversionFlowAmount *big.Int
	MinerDifficulty      *big.Int
}
