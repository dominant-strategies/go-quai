package quaiapi

import (
	"context"
	"errors"
	"runtime/debug"
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
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
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
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	var wg sync.WaitGroup
	wg.Add(len(endpoints))

	for _, endpoint := range endpoints {
		go func(endpoint string) {
			defer func() {
				if r := recover(); r != nil {
					log.Global.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
					}).Error("Go-Quai Panicked")
				}
			}()
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
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
				}).Error("Go-Quai Panicked")
			}
		}()
		wg.Wait()
		close(clientCh)
	}()
}

func (worker *TxWorker) processTxs(ctx context.Context, resultCh chan *types.WorkObject) {
	defer func() {
		if r := recover(); r != nil {
			log.Global.WithFields(log.Fields{
				"error":      r,
				"stacktrace": string(debug.Stack()),
			}).Error("Go-Quai Panicked")
		}
	}()
	for {
		select {
		case tx, ok := <-worker.txChan:
			if !ok {
				log.Global.Error("Tx miner channel was somehow closed")
			}
			worker.engine.MineToThreshold(tx, worker.threshold, ctx.Done(), resultCh)
		case <-ctx.Done():
			return
		case workedTx := <-resultCh:
			worker.clients[0].SubmitSubWorkshare(ctx, workedTx)
		}
	}
}

func (worker *TxWorker) AddTransaction(tx *types.Transaction) error {
	threshold, err := worker.clients[0].GetWorkShareThreshold(context.Background())
	if err != nil {
		return err
	}
	worker.threshold = threshold

	pendingWo, err := worker.clients[0].GetPendingHeader(context.Background())
	if err != nil {
		return err
	}
	worker.workTemplate = pendingWo
	pendingWo = types.CopyWorkObject(pendingWo)

	select {
	case worker.txChan <- pendingWo:
		return nil
	default:
		return errChanFull
	}
}

func (worker *TxWorker) StopWorkers() {
	worker.cancelFunc()
}
