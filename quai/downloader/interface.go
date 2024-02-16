package downloader

import (
	"github.com/dominant-strategies/go-quai/common"
)

type P2PNode interface {
	// Method to request data from the network
	// Specify location, data hash, and data type to request
	Request(common.Location, interface{}, interface{}) chan interface{}
}
