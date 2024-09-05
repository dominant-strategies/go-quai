package quaiapi

import (
	"context"
	"errors"
	"sync"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/consensus"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/log"
	"github.com/dominant-strategies/go-quai/quaiclient"
)

const (
	c_numWorkers    = 5
	c_numWaitingTxs = 10
)

var (
	errChanFull = errors.New("channel is full")
)

type TxWorker struct {
	engine     consensus.Engine
	clients    []*quaiclient.Client
	location   common.Location
	api        *PublicWorkSharesAPI
	txChan     chan *types.WorkObject
	cancelFunc context.CancelFunc

	threshold    int
	workTemplate *types.WorkObject
}

func StartTxWorker(engine consensus.Engine, endpoints []string, location common.Location, api *PublicWorkSharesAPI, workShareThreshold int) *TxWorker {
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan *types.WorkObject)

	clientCh := make(chan *quaiclient.Client)
	go dialClientsAsync(endpoints, clientCh)

	worker := &TxWorker{
		engine:     engine,
		clients:    []*quaiclient.Client{},
		location:   location,
		api:        api,
		txChan:     make(chan *types.WorkObject, c_numWaitingTxs),
		cancelFunc: cancel,

		threshold: workShareThreshold,
	}

	go func() {
		for client := range clientCh {
			worker.clients = append(worker.clients, client)
		}
		log.Global.Info("Finished connecting miner endpoints")
	}()

	// Kick off worker threads
	for i := 0; i < c_numWorkers; i++ {
		go worker.processTxs(ctx, resultCh)
	}

	return worker
}

func dialClientsAsync(endpoints []string, clientCh chan *quaiclient.Client) {
	var wg sync.WaitGroup
	wg.Add(len(endpoints))

	for _, endpoint := range endpoints {
		go func(endpoint string) {
			defer wg.Done()
			client, err := quaiclient.Dial(endpoint, log.Global)
			if err != nil {
				log.Global.WithField("endpoint", endpoint).Error("Unable to dial miner endpoint")
			} else {
				clientCh <- client
			}
		}(endpoint)
	}

	go func() {
		wg.Wait()
		close(clientCh)
	}()
}
