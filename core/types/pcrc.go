package types

import "github.com/spruce-solutions/go-quai/common"

// PRTP = prime terminus in prime on any region
// PRTR = prime terminus in region
// PTP = prime terminus in prime
// PTR = prime terminus in region
// PTZ = prime terminus in zone
// RTR = region terminus in region
// RTZ = region terminus in zone
type PCRCTermini struct {
	PRTP common.Hash
	PRTR common.Hash

	PTP common.Hash
	PTR common.Hash
	PTZ common.Hash

	RTR common.Hash
	RTZ common.Hash
}
