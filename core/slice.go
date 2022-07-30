package core

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/spruce-solutions/go-quai/common"
	"github.com/spruce-solutions/go-quai/consensus"
	"github.com/spruce-solutions/go-quai/core/types"
	"github.com/spruce-solutions/go-quai/core/vm"
	"github.com/spruce-solutions/go-quai/ethclient/quaiclient"
	"github.com/spruce-solutions/go-quai/ethdb"
	"github.com/spruce-solutions/go-quai/log"
	"github.com/spruce-solutions/go-quai/params"
)

type Slice struct {
	hc *HeaderChain

	config *params.ChainConfig
	engine consensus.Engine

	domClient  *quaiclient.Client   // domClient is used to check if a given dominant block in the chain is canonical in dominant chain.
	subClients []*quaiclient.Client // subClinets is used to check is a coincident block is valid in the subordinate context
}

func NewSlice(db ethdb.Database, cacheConfig *CacheConfig, chainConfig *params.ChainConfig, domClientUrl string, subClientUrls []string, engine consensus.Engine, vmConfig vm.Config, shouldPreserve func(header *types.Header) bool, txLookupLimit *uint64) (*Slice, error) {

	sl := &Slice{
		config: chainConfig,
		engine: engine,
	}

	var err error
	sl.hc, err = NewHeaderChain(db, cacheConfig, chainConfig, domClientUrl, subClientUrls, engine, vmConfig, shouldPreserve, txLookupLimit)
	if err != nil {
		return nil, err
	}

	// only set the domClient if the chain is not prime
	if types.QuaiNetworkContext != params.PRIME {
		sl.domClient = MakeDomClient(domClientUrl)
	}

	sl.subClients = make([]*quaiclient.Client, 3)
	// only set the subClients if the chain is not region
	if types.QuaiNetworkContext != params.ZONE {
		go func() {
			sl.subClients = MakeSubClients(subClientUrls)
		}()
	}

	return sl, nil
}

// MakeDomClient creates the quaiclient for the given domurl
func MakeDomClient(domurl string) *quaiclient.Client {
	if domurl == "" {
		log.Crit("dom client url is empty")
	}
	domClient, err := quaiclient.Dial(domurl)
	if err != nil {
		log.Crit("Error connecting to the dominant go-quai client", "err", err)
	}
	return domClient
}

// MakeSubClients creates the quaiclient for the given suburls
func MakeSubClients(suburls []string) []*quaiclient.Client {
	subClients := make([]*quaiclient.Client, 3)
	for i, suburl := range suburls {
		if suburl == "" {
			log.Warn("sub client url is empty")
		}
		subClient, err := quaiclient.Dial(suburl)
		if err != nil {
			log.Crit("Error connecting to the subordinate go-quai client for index", "index", i, " err ", err)
		}
		subClients[i] = subClient
	}
	return subClients
}

// Config retrieves the slice's chain configuration.
func (sl *Slice) Config() *params.ChainConfig { return sl.config }

// Engine retrieves the header chain's consensus engine.
func (sl *Slice) Engine() consensus.Engine { return sl.engine }

// The purpose of the Previous Coincident Reference Check (PCRC) is to establish
// that we have linked untwisted chains prior to checking HLCR & applying external state transfers.
// NOTE: note that it only guarantees linked & untwisted back to the prime terminus, assuming the
// prime termini match. To check deeper than that, you need to iteratively apply PCRC to get that guarantee.
func (sl *Slice) PCRC(header *types.Header, headerOrder int) (types.PCRCTermini, error) {

	if header.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
		return types.PCRCTermini{}, nil
	}

	slice := header.Location
	// Prime twist check
	// PTZ -- Prime coincident along zone path
	// PTR -- Prime coincident along region path
	// PTP -- Prime coincident along prime path
	// Region twist check
	// RTZ -- Region coincident along zone path
	// RTR -- Region coincident along region path

	// o/c			| prime 			| region 						| zone
	// prime    	| x PTP, RTR		| x PTP, RTR					| x PTP, PTR, RTR
	// region   	| X					| x PTP, RTR, PRTP, PRTR		| x PTP, PTR, RTR, PRTP, PRTR
	// zone			| X					| X								| x PTP, PTR, RTR, PRTP, PRTR

	switch types.QuaiNetworkContext {
	case params.PRIME:
		fmt.Println("PCRC Running PTP")
		PTP, err := sl.PreviousValidCoincidentOnPath(header, slice, params.PRIME, params.PRIME, true)
		fmt.Println("Hash: PTP", PTP.Hash(), "error:", err)
		if err != nil {
			return types.PCRCTermini{}, err
		}
		fmt.Println("PCRC Running PRTP")
		PRTP, err := sl.PreviousValidCoincidentOnPath(header, slice, params.PRIME, params.PRIME, false)
		fmt.Println("Hash: PRTP", PRTP.Hash(), "error:", err)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if sl.subClients[slice[0]-1] == nil {
			return types.PCRCTermini{}, nil
		}
		PCRCTermini, err := sl.subClients[slice[0]-1].CheckPCRC(context.Background(), header, headerOrder)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if (PCRCTermini.PTR == common.Hash{} || PCRCTermini.PRTR == common.Hash{}) {
			fmt.Println("nil escape in PCRC, PTR:", PCRCTermini.PTR, "PRTR:", PCRCTermini.PRTR)
			return PCRCTermini, consensus.ErrSliceNotSynced
		}

		PCRCTermini.PTP = PTP.Hash()
		PCRCTermini.PRTP = PRTP.Hash()

		if (PTP.Hash() != PCRCTermini.PTR) && (PCRCTermini.PTR != PCRCTermini.PTZ) && (PCRCTermini.PTZ != PTP.Hash()) {
			fmt.Println("PTP", PTP.Hash(), "PTR", PCRCTermini.PTR, "PTZ", PCRCTermini.PTZ)
			return types.PCRCTermini{}, errors.New("there exists a Prime twist (PTP != PTR != PTZ")
		}
		if PRTP.Hash() != PCRCTermini.PRTR {
			fmt.Println("PRTP", PRTP.Hash(), PCRCTermini.PRTR)
			return types.PCRCTermini{}, errors.New("there exists a Prime twist (PRTP != PRTR")
		}

		return PCRCTermini, nil

	case params.REGION:
		fmt.Println("PCRC Running RTR")
		RTR, err := sl.PreviousValidCoincidentOnPath(header, slice, params.REGION, params.REGION, true)
		fmt.Println("Hash: RTR", RTR.Hash(), "error:", err)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if sl.subClients[slice[1]-1] == nil {
			return types.PCRCTermini{}, nil
		}

		PCRCTermini, err := sl.subClients[slice[1]-1].CheckPCRC(context.Background(), header, headerOrder)
		if err != nil {
			return types.PCRCTermini{}, err
		}

		if (PCRCTermini.RTZ == common.Hash{}) {
			return PCRCTermini, consensus.ErrSliceNotSynced
		}

		if RTR.Hash() != PCRCTermini.RTZ {
			fmt.Println("RTR", RTR.Number, RTR.Hash(), "RTZ", PCRCTermini.RTZ)
			return types.PCRCTermini{}, errors.New("there exists a Region twist (RTR != RTZ)")
		}
		if headerOrder < params.REGION {
			fmt.Println("PCRC Running PTR")
			PTR, err := sl.PreviousValidCoincidentOnPath(header, slice, params.PRIME, params.REGION, true)
			fmt.Println("Hash: PTR", PTR.Hash(), "error:", err)
			if err != nil {
				return types.PCRCTermini{}, err
			}
			fmt.Println("PCRC Running PRTR")
			PRTR, err := sl.PreviousValidCoincidentOnPath(header, slice, params.PRIME, params.REGION, false)
			fmt.Println("Hash: PRTR", PRTR.Hash(), "error:", err)
			if err != nil {
				return types.PCRCTermini{}, err
			}

			PCRCTermini.PTR = PTR.Hash()
			PCRCTermini.PRTR = PRTR.Hash()
		}
		return PCRCTermini, nil

	case params.ZONE:
		PCRCTermini := types.PCRCTermini{}

		// only compute PTZ and RTZ on the coincident block in zone.
		// PTZ and RTZ are essentially a signaling mechanism to know that we are building on the right terminal header.
		// So running this only on a coincident block makes sure that the zones can move and sync past the coincident.
		// Just run RTZ to make sure that its linked. This check decouples this signaling and linking paradigm.

		fmt.Println("PCRC Running PTZ")
		PTZ, err := sl.PreviousValidCoincidentOnPath(header, slice, params.PRIME, params.ZONE, true)
		fmt.Println("Hash: PTZ", PTZ.Hash(), "error:", err)
		if err != nil {
			return types.PCRCTermini{}, err
		}
		PCRCTermini.PTZ = PTZ.Hash()

		fmt.Println("PCRC Running RTZ")
		RTZ, err := sl.PreviousValidCoincidentOnPath(header, slice, params.REGION, params.ZONE, true)
		fmt.Println("Hash: RTZ", RTZ.Hash(), "error:", err)
		if err != nil {
			return types.PCRCTermini{}, err
		}
		PCRCTermini.RTZ = RTZ.Hash()

		return PCRCTermini, nil
	}
	return types.PCRCTermini{}, errors.New("running in unsupported context")
}

// PreviousValidCoincidentOnPath searches the path for a cononical block of specified order in the specified slice
//     *slice - The zone location which defines the slice in which we are validating
//     *order - The order of the conincidence that is desired
//     *path - Search among ancestors of this path in the specified slice
func (sl *Slice) PreviousValidCoincidentOnPath(header *types.Header, slice []byte, order, path int, fullSliceEqual bool) (*types.Header, error) {
	prevTerminalHeader := header
	for {
		if prevTerminalHeader.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
			return sl.hc.GetHeaderByHash(sl.Config().GenesisHashes[0]), nil
		}

		terminalHeader, err := sl.Engine().PreviousCoincidentOnPath(sl.hc, prevTerminalHeader, slice, order, path, fullSliceEqual)
		if err != nil {
			return nil, err
		}

		fmt.Println("Running PVCOP for header: ", header.Hash(), header.Number, "terminal Header", terminalHeader.Hash(), terminalHeader.Number)

		if terminalHeader.Number[types.QuaiNetworkContext].Cmp(big.NewInt(0)) == 0 {
			return sl.hc.GetHeaderByHash(sl.Config().GenesisHashes[0]), nil
		}

		// If the current header is dominant coincident check the status with the dom node
		if order < types.QuaiNetworkContext {
			status := sl.domClient.GetBlockStatus(context.Background(), terminalHeader)
			fmt.Println("terminal Header status", status)
			if status == quaiclient.CanonStatTy {
				if prevTerminalHeader.Hash() != header.Hash() {
					return nil, errors.New("subordinate terminus mismatch")
				}
				return terminalHeader, nil
			}
		} else if order == types.QuaiNetworkContext {
			return terminalHeader, err
		}

		prevTerminalHeader = terminalHeader
	}
}
