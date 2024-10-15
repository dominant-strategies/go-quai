package protocol

import (
	"github.com/libp2p/go-libp2p/core/protocol"
)

const (
	// ProtocolVersion is the current version of the Quai protocol
	ProtocolVersion protocol.ID = "/quai/1.0.0"

	// Block height before which the prior major release will be tolerated
	//
	// For example, if the current protocol version is `quai/9.1.2`, and
	// `ProtocolGraceHeight` is 10000000, then peers running any `quai/9.X.X`
	// protocol version will still be tolerated. After the grace period is over,
	// peers running a protocol less than `quai/9.0.0` will not be tolerated.
	//
	// Note, in this example, peers below `quai/8.0.0` are not tolerated even
	// during the grace period.
	ProtocolGraceHeight uint64 = 0
)
