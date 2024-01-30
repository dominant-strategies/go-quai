package quai

import (
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/quaiapi"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/p2p"
)

// QuaiBackend implements the quai consensus protocol
type QuaiBackend struct {
	primeApiBackend   *quaiapi.Backend
	regionApiBackends []*quaiapi.Backend
	zoneApiBackends   [][]*quaiapi.Backend
}

// Create a new instance of the QuaiBackend consensus service
func NewQuaiBackend() (*QuaiBackend, error) {
	zoneBackends := make([][]*quaiapi.Backend, 1)
	for i := 0; i < 1; i++ {
		zoneBackends[i] = make([]*quaiapi.Backend, 1)
	}
	return &QuaiBackend{regionApiBackends: make([]*quaiapi.Backend, 1), zoneApiBackends: zoneBackends}, nil
}

func (qbe *QuaiBackend) SetApiBackend(apiBackend quaiapi.Backend, location common.Location) {
	switch location.Context() {
	case common.PRIME_CTX:
		qbe.SetPrimeApiBackend(apiBackend)
	case common.REGION_CTX:
		qbe.SetRegionApiBackend(apiBackend, location)
	case common.ZONE_CTX:
		qbe.SetZoneApiBackend(apiBackend, location)
	}
}

// Set the PrimeBackend into the QuaiBackend
func (qbe *QuaiBackend) SetPrimeApiBackend(primeBackend quaiapi.Backend) {
	qbe.primeApiBackend = &primeBackend
}

// Set the RegionBackend into the QuaiBackend
func (qbe *QuaiBackend) SetRegionApiBackend(regionBackend quaiapi.Backend, location common.Location) {
	qbe.regionApiBackends[location.Region()] = &regionBackend
}

// Set the ZoneBackend into the QuaiBackend
func (qbe *QuaiBackend) SetZoneApiBackend(zoneBackend quaiapi.Backend, location common.Location) {
	qbe.zoneApiBackends[location.Region()][location.Zone()] = &zoneBackend
}

func (qbe *QuaiBackend) GetBackend(location common.Location) *quaiapi.Backend {
	switch location.Context() {
	case common.PRIME_CTX:
		return qbe.primeApiBackend
	case common.REGION_CTX:
		return qbe.regionApiBackends[location.Region()]
	case common.ZONE_CTX:
		return qbe.zoneApiBackends[location.Region()][location.Zone()]
	}
	return nil
}

// Handle consensus data propagated to us from our peers
func (qbe *QuaiBackend) OnNewBroadcast(sourcePeer p2p.PeerID, data interface{}, nodeLocation common.Location) bool {
	switch data.(type) {
	case types.Block:
		block := data.(types.Block)
		backend := *qbe.GetBackend(nodeLocation)
		if backend == nil {
			log.Global.Error("no backend found")
			return false
		}
		backend.WriteBlock(&block)
		return true
	case types.Header:
	case types.Transaction:
	}
	return true
}

// Returns the current block height for the given location
func (qbe *QuaiBackend) GetHeight(location common.Location) uint64 {
	// Example/mock implementation
	panic("todo")
}

func (qbe *QuaiBackend) LookupBlock(hash common.Hash, location common.Location) *types.Block {
	panic("todo")
}
