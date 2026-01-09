package pubsubManager

import (
	"context"
	"errors"
	"math/big"
	"runtime/debug"
	"sync"
	"time"

	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
	"lukechampine.com/blake3"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/internal/telemetry"
	"github.com/dominant-strategies/go-quai/log"
	p2p "github.com/dominant-strategies/go-quai/p2p"
	"github.com/dominant-strategies/go-quai/p2p/pb"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai"
	expireLru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	numWorkers                 = 20   // Number of workers per stream
	msgChanSize                = 1000 // 1000 requests per subscription
	c_MaxWorkShareDist         = 5
	c_BroadcastCacheSize       = 1000
	c_BroadcastCacheExpiryTime = 3 * time.Second
)

var (
	ErrConsensusNotSet     = errors.New("consensus backend not set")
	ErrValidatorFuncNotSet = errors.New("validator function cannot be initialized")
	ErrNoTopic             = errors.New("no topic for requested data")
	ErrUnsupportedType     = errors.New("data type not supported")
)

type PubsubManager struct {
	*pubsub.PubSub
	ctx           context.Context
	subscriptions *sync.Map
	topics        *sync.Map
	consensus     quai.ConsensusAPI
	genesis       common.Hash

	broadcastCache *expireLru.LRU[string, struct{}]

	// Callback function to handle received data
	onReceived func(peer.ID, string, string, interface{}, common.Location)
}

// creates a new gossipsub instance
// TODO: what options do we need for quai network? See:
// See https://pkg.go.dev/github.com/libp2p/go-libp2p-pubsub@v0.10.0#Option
func NewGossipSubManager(ctx context.Context, h host.Host) (*PubsubManager, error) {
	cfg := pubsub.DefaultGossipSubParams()
	cfg.D = 30
	cfg.Dlo = 6
	cfg.Dhi = 45
	cfg.Dout = 20
	ps, err := pubsub.NewGossipSub(ctx, h, pubsub.WithGossipSubParams(cfg), pubsub.WithMaxMessageSize(params.MaxGossipsubPacketSize))
	if err != nil {
		return nil, err
	}

	// create a broadcast cache to not broadcast duplicate messages per topic
	broadcastCache := expireLru.NewLRU[string, struct{}](c_BroadcastCacheSize, nil, c_BroadcastCacheExpiryTime)

	return &PubsubManager{
		ps,
		ctx,
		new(sync.Map),
		new(sync.Map),
		nil,
		utils.MakeGenesis().ToBlock(0).Hash(),
		broadcastCache,
		nil,
	}, nil
}

func (g *PubsubManager) GetGenesis() common.Hash {
	return g.genesis
}

func (g *PubsubManager) SetQuaiBackend(consensus quai.ConsensusAPI) {
	g.UnsubscribeAll()      // First unsubscribe from existing topics, if already registered
	g.consensus = consensus // Set new backend

}

func (g *PubsubManager) SetReceiveHandler(receiveCb func(peer.ID, string, string, interface{}, common.Location)) {
	g.onReceived = receiveCb
}

func (g *PubsubManager) Stop() error {
	g.UnsubscribeAll()
	return nil
}

func (g *PubsubManager) UnsubscribeAll() {
	g.subscriptions.Range(func(key, value any) bool {
		value.(*pubsub.Subscription).Cancel()
		g.subscriptions.Delete(key)
		return true
	})
	g.topics.Range(func(key, value any) bool {
		value.(*pubsub.Topic).Close()
		g.topics.Delete(key)
		return true
	})
}

func (g *PubsubManager) SubscribeAndRegisterValidator(location common.Location, datatype interface{}, validatorFunc pubsub.ValidatorEx) error {
	// build topic name
	topicSub, err := NewTopic(g.genesis, location, datatype)
	if err != nil {
		return err
	}
	err = g.Subscribe(topicSub, location, datatype)
	if err != nil {
		return err
	}

	var validator pubsub.ValidatorEx
	if validatorFunc == nil {
		validator = g.ValidatorFunc()
	} else {
		validator = validatorFunc
	}

	err = g.PubSub.RegisterTopicValidator(topicSub.String(), validator)
	if err != nil {
		return err
	}

	return nil
}

// subscribe to broadcasts of the given type of data
func (g *PubsubManager) Subscribe(topicSub *Topic, location common.Location, datatype interface{}) error {
	// join the topic
	topic, err := g.Join(topicSub.String())
	if err != nil {
		return err
	}
	g.topics.Store(topicSub.String(), topic)
	if g.consensus == nil {
		return ErrConsensusNotSet
	}

	// subscribe to the topic
	subscription, err := topic.Subscribe(pubsub.WithBufferSize(msgChanSize))
	if err != nil {
		return err
	}
	g.subscriptions.Store(topicSub.String(), subscription)

	go func(location common.Location, sub *pubsub.Subscription) {
		defer func() {
			if r := recover(); r != nil {
				log.Global.WithFields(log.Fields{
					"error":      r,
					"stacktrace": string(debug.Stack()),
					"location":   location.Name(),
				}).Fatal("Go-Quai Panicked")
			}
		}()
		// Create a channel for messages
		msgChan := make(chan *pubsub.Message, msgChanSize)
		// close the msgChan if we exit this function
		defer close(msgChan)
		full := 0
		// maintain a number of worker threads to handle messages
		var msgWorker func(location common.Location)
		msgWorker = func(location common.Location) {
			defer func() {
				if r := recover(); r != nil {
					log.Global.WithFields(log.Fields{
						"error":      r,
						"stacktrace": string(debug.Stack()),
						"location":   location.Name(),
					}).Errorf("Go-Quai Panicked")
				}
				go msgWorker(location) // If this worker exits, start a new one
			}()
			for msg := range msgChan { // This should exit when msgChan is closed
				data := types.ObjectPool.Get()
				data = nil
				// unmarshal the received data depending on the topic's type
				err = pb.UnmarshalAndConvert(msg.Data, location, &data, datatype)
				if err != nil {
					log.Global.Errorf("error unmarshalling data: %s", err)
					continue
				}

				// handle the received data
				if g.onReceived != nil {
					g.onReceived(msg.ReceivedFrom, msg.ID, *msg.Topic, data, location)
				}
			}
		}
		for i := 0; i < numWorkers; i++ {
			go msgWorker(location)
		}
		log.Global.WithField("topic", topic.String()).Debugf("Subscribed to topic")
		for {
			msg, err := sub.Next(g.ctx)
			if err != nil || msg == nil {
				// if context or subscription was cancelled, then we are shutting down
				if g.ctx.Err() != nil || err == pubsub.ErrSubscriptionCancelled {
					return
				}
				log.Global.Errorf("error getting next message from subscription: %s", err)
				continue
			}
			log.Global.Tracef("received message on topic: %s", topicSub.String())

			// Send to worker goroutines
			select {
			case msgChan <- msg:
			default:
				if full%1000 == 0 {
					log.Global.WithField("topic", topicSub.String()).Warnf("message channel full. Lost messages: %d", full)
				}
				full++
			}
		}
	}(location, subscription)

	return nil
}

func (g *PubsubManager) ValidatorFunc() func(ctx context.Context, id p2p.PeerID, msg *pubsub.Message) pubsub.ValidationResult {
	return func(ctx context.Context, id peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
		var data interface{}
		topicString := msg.Topic
		if topicString == nil {
			return pubsub.ValidationReject
		}
		topic, err := TopicFromString(*topicString)
		if err != nil {
			log.Global.WithField("err", err).Error("Error calculating TopicFromString")
			return pubsub.ValidationReject
		}
		// get the proto encoded data
		protoData := msg.GetData()

		// get the topic data to be used to decode the proto data
		data = topic.data

		switch data.(type) {
		case *types.WorkObjectBlockView:

			protoWo := new(types.ProtoWorkObjectBlockView)
			err := proto.Unmarshal(protoData, protoWo)
			if err != nil {
				log.Global.WithField("err", err).Error("Error unmarshalling proto wo block view")
				return pubsub.ValidationReject
			}

			block := &types.WorkObjectBlockView{
				WorkObject: &types.WorkObject{},
			}
			err = block.ProtoDecode(protoWo, protoWo.GetWorkObject().GetWoHeader().GetLocation().Value)
			if err != nil {
				log.Global.WithField("err", err).Error("Error proto decode wo block view")
				return pubsub.ValidationReject
			}

			backend := *g.consensus.GetBackend(topic.location)
			if backend == nil {
				log.Global.WithFields(log.Fields{
					"peer":     id,
					"hash":     block.Hash(),
					"location": block.Location(),
				}).Error("no backend found for this location")
			}
			err = backend.SanityCheckWorkObjectBlockViewBody(block.WorkObject)
			if err != nil {
				backend.Logger().WithField("err", err).Warn("Sanity check of work object failed")
				return pubsub.ValidationReject
			}
			if backend.BadHashExistsInChain() {
				backend.Logger().Warn("Bad Hashes still exist on chain, cannot handle block broadcast yet")
				return pubsub.ValidationIgnore
			}

			// If Block broadcasted by the peer exists in the bad block list drop the peer
			if backend.IsBlockHashABadHash(block.WorkObjectHeader().Hash()) {
				log.Global.WithField("err", err).Error("Work object block hash is a bad hash")
				return pubsub.ValidationReject
			}

			if block.AuxPow() != nil && !block.WorkObject.AuxPow().ConvertToTemplate().VerifySignature() {
				backend.Logger().Warn("Work share auxpow signature verification failed")
				return pubsub.ValidationReject
			}

			return backend.ApplyPoWFilter(block.WorkObject)

		case *types.WorkObjectHeaderView:

			protoWo := new(types.ProtoWorkObjectHeaderView)
			err := proto.Unmarshal(protoData, protoWo)
			if err != nil {
				log.Global.WithField("err", err).Error("Error unmarshalling proto wo header view")
				return pubsub.ValidationReject
			}

			block := &types.WorkObjectHeaderView{
				WorkObject: &types.WorkObject{},
			}
			err = block.ProtoDecode(protoWo, protoWo.GetWorkObject().GetWoHeader().GetLocation().Value)
			if err != nil {
				log.Global.WithField("err", err).Error("Error proto decode wo header view")
				return pubsub.ValidationReject
			}

			backend := *g.consensus.GetBackend(topic.location)
			if backend == nil {
				log.Global.WithFields(log.Fields{
					"peer":     id,
					"hash":     block.Hash(),
					"location": block.Location(),
				}).Error("no backend found for this location")
			}
			err = backend.SanityCheckWorkObjectHeaderViewBody(block.WorkObject)
			if err != nil {
				backend.Logger().WithField("err", err).Warn("Sanity check of work object header view failed")
				return pubsub.ValidationReject
			}
			if backend.BadHashExistsInChain() {
				backend.Logger().Warn("Bad Hashes still exist on chain, cannot handle block broadcast yet")
				return pubsub.ValidationIgnore
			}

			if block.AuxPow() != nil && !block.WorkObject.AuxPow().ConvertToTemplate().VerifySignature() {
				backend.Logger().Warn("Work share auxpow signature verification failed")
				return pubsub.ValidationReject
			}

			// If Block broadcasted by the peer exists in the bad block list drop the peer
			if backend.IsBlockHashABadHash(block.WorkObject.WorkObjectHeader().Hash()) {
				log.Global.WithField("err", err).Error("Work object header hash is a bad hash")
				return pubsub.ValidationReject
			}
			return backend.ApplyPoWFilter(block.WorkObject)

		case *types.WorkObjectShareView:
			protoWo := new(types.ProtoWorkObjectShareView)
			err := proto.Unmarshal(protoData, protoWo)
			if err != nil {
				log.Global.WithField("err", err).Error("Error unmarshalling proto wo share view")
				return pubsub.ValidationReject
			}

			block := &types.WorkObjectShareView{
				WorkObject: &types.WorkObject{},
			}

			err = block.ProtoDecode(protoWo, protoWo.GetWorkObject().GetWoHeader().GetLocation().Value)
			if err != nil {
				log.Global.WithField("err", err).Error("Error proto decode proto wo share view")
				return pubsub.ValidationReject
			}

			backend := *g.consensus.GetBackend(topic.location)
			if backend == nil {
				log.Global.WithFields(log.Fields{
					"peer":     id,
					"hash":     block.Hash(),
					"location": block.Location(),
				}).Error("no backend found for this location")
			}

			if backend.NodeCtx() == common.ZONE_CTX && backend.ChainConfig().TelemetryEnabled {
				// If broadcasting a workshare, track it as received (by network)
				if wsv, ok := data.(*types.WorkObjectShareView); ok && wsv != nil && wsv.WorkObject != nil && wsv.WorkObject.WorkObjectHeader() != nil {
					telemetry.RecordReceivedHeader(wsv.WorkObject.WorkObjectHeader())
				}
			}

			// check if the work share is valid before accepting the transactions
			// from the peer
			err = backend.SanityCheckWorkObjectShareViewBody(block.WorkObject)
			if err != nil {
				backend.Logger().WithField("err", err).Warn("Sanity check of work object share view failed")
				return pubsub.ValidationReject
			}

			currentHeader := backend.CurrentHeader()

			// Check that the work share is atmost c_MaxWorkShareDist behind
			if block.WorkObject.NumberU64(backend.NodeCtx())+c_MaxWorkShareDist < currentHeader.NumberU64(backend.NodeCtx()) {
				return pubsub.ValidationIgnore
			}

			// Check that the workshare is atmost c_MaxWorkShareDist ahead
			if block.WorkObject.NumberU64(backend.NodeCtx()) > currentHeader.NumberU64(backend.NodeCtx())+c_MaxWorkShareDist {
				return pubsub.ValidationIgnore
			}

			if len(block.WorkObject.Transactions()) > int(backend.GetMaxTxInWorkShare()) {
				backend.Logger().Errorf("workshare contains more transactions than allowed. Max allowed: %d, actual: %d", backend.GetMaxTxInWorkShare(), len(block.WorkObject.Transactions()))
				return pubsub.ValidationReject
			}

			if block.AuxPow() != nil {

				scryptSig := types.ExtractScriptSigFromCoinbaseTx(block.WorkObject.AuxPow().Transaction())

				signatureTime, err := types.ExtractSignatureTimeFromCoinbase(scryptSig)
				if err != nil {
					backend.Logger().WithField("err", err).Error("signature time not found in the auxpow")
					return pubsub.ValidationReject
				}
				// auxpow header time and quai block time cannot be less than the
				// signature time in the coinbase (time at which the template was
				// signed)
				if block.WorkObject.AuxPow().Header().Timestamp() < signatureTime {
					backend.Logger().WithFields(log.Fields{
						"auxpowHeaderTime": block.WorkObject.AuxPow().Header().Timestamp(),
						"signatureTime":    signatureTime,
					}).Error("auxpowheader time is less than signature time")
					return pubsub.ValidationReject
				}
				if block.WorkObject.Time() < uint64(signatureTime) {
					backend.Logger().WithFields(log.Fields{
						"auxpowHeaderTime": block.WorkObject.Time(),
						"signatureTime":    signatureTime,
					}).Error("quai block time is less than signature time")
					return pubsub.ValidationReject
				}

				coinbaseSealHash, err := types.ExtractSealHashFromCoinbase(scryptSig)
				if err != nil {
					backend.Logger().WithField("err", err).Error("coinbase seal hash not found in the auxpow")
					return pubsub.ValidationReject
				}

				switch block.WorkObject.AuxPow().PowID() {
				case types.Kawpow, types.SHA_BTC, types.SHA_BCH:
					if block.WorkObject.SealHash() != coinbaseSealHash {
						backend.Logger().WithFields(log.Fields{
							"expectedSealHash": block.WorkObject.SealHash(),
							"actualSealHash":   coinbaseSealHash,
						}).Error("coinbase seal hash does not match uncle seal hash")
						return pubsub.ValidationReject
					}
				case types.Scrypt:
					// Since litecoin is merged mined with dogecoin, merkle root
					// needs to be calculated
					dogeHash := common.Hash(block.WorkObject.AuxPow().AuxPow2())
					if (dogeHash == common.Hash{}) {
						backend.Logger().Error("doge hash is nil in scrypt auxpow")
						return pubsub.ValidationReject
					}
					auxMerkleRoot := types.CreateAuxMerkleRoot(dogeHash, block.WorkObject.SealHash())
					if auxMerkleRoot != coinbaseSealHash {
						backend.Logger().WithFields(log.Fields{
							"expectedAuxMerkleRoot":  auxMerkleRoot,
							"actualCoinbaseSealHash": coinbaseSealHash,
						}).Error("coinbase seal hash does not match uncle aux merkle root")
						return pubsub.ValidationReject
					}
					merkleSize, merkleNonce, err := types.ExtractMerkleSizeAndNonceFromCoinbase(scryptSig)
					if err != nil {
						backend.Logger().WithField("err", err).Error("failed to extract merkle size and nonce from coinbase")
						return pubsub.ValidationReject
					}
					if merkleSize != params.MerkleSize {
						backend.Logger().WithFields(log.Fields{
							"expectedMerkleSize": params.MerkleSize,
							"actualMerkleSize":   merkleSize,
						}).Error("invalid merkle size in coinbase")
						return pubsub.ValidationReject
					}
					if merkleNonce != params.MerkleNonce {
						backend.Logger().WithFields(log.Fields{
							"expectedMerkleNonce": params.MerkleNonce,
							"actualMerkleNonce":   merkleNonce,
						}).Error("invalid merkle nonce in coinbase")
						return pubsub.ValidationReject
					}
				}

				// Verify the merkle root as well
				expectedMerkleRoot := types.CalculateMerkleRoot(block.WorkObject.AuxPow().PowID(), block.WorkObject.AuxPow().Transaction(), block.WorkObject.AuxPow().MerkleBranch())
				if block.WorkObject.AuxPow().Header().MerkleRoot() != expectedMerkleRoot {
					backend.Logger().WithFields(log.Fields{
						"expectedMerkleRoot": expectedMerkleRoot,
						"actualMerkleRoot":   block.WorkObject.AuxPow().Header().MerkleRoot(),
					}).Error("invalid merkle root in auxpow")
					return pubsub.ValidationReject
				}

				if err := types.ValidatePrevOutPointIndexAndSequenceOfCoinbase(block.WorkObject.AuxPow().Transaction()); err != nil {
					backend.Logger().WithField("err", err).Error("invalid prev out point index and sequence in coinbase transaction")
					return pubsub.ValidationReject
				}

				if !block.WorkObject.AuxPow().ConvertToTemplate().VerifySignature() && !block.WorkObjectHeader().IsShaOrScryptShareWithInvalidAddress() {
					backend.Logger().Warn("Work share auxpow signature verification failed")
					return pubsub.ValidationReject
				}

			}

			// If the workshare is of sha or scrypt the work validation is different than the progpow or kawpow
			if block.WorkObject.WorkObjectHeader().KawpowActivationHappened() &&
				block.WorkObject.AuxPow() != nil &&
				(block.WorkObject.AuxPow().PowID() == types.SHA_BCH ||
					block.WorkObject.AuxPow().PowID() == types.SHA_BTC ||
					block.WorkObject.AuxPow().PowID() == types.Scrypt) {

				// If the current header is not a block after kawpow activation,
				// node cannot do entropy checks
				if !currentHeader.WorkObjectHeader().KawpowActivationHappened() {
					return pubsub.ValidationIgnore
				}

				var powHash common.Hash
				var workShareTarget *big.Int
				var shareDiff, currentHeaderShareDiff, thresholdShareDiff *big.Int
				if block.WorkObject.AuxPow().PowID() == types.Scrypt {
					workShareTarget = new(big.Int).Div(common.Big2e256, block.WorkObject.WorkObjectHeader().ScryptDiffAndCount().Difficulty())
					powHash = block.WorkObject.AuxPow().Header().PowHash()
					shareDiff = block.WorkObject.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
					currentHeaderShareDiff = currentHeader.WorkObjectHeader().ScryptDiffAndCount().Difficulty()
					thresholdShareDiff = new(big.Int).Div(new(big.Int).Mul(currentHeaderShareDiff, params.ShareDiffRelativeThreshold), big.NewInt(100))
				} else {
					workShareTarget = new(big.Int).Div(common.Big2e256, block.WorkObject.WorkObjectHeader().ShaDiffAndCount().Difficulty())
					powHash = block.WorkObject.AuxPow().Header().PowHash()
					shareDiff = block.WorkObject.WorkObjectHeader().ShaDiffAndCount().Difficulty()
					currentHeaderShareDiff = currentHeader.WorkObjectHeader().ShaDiffAndCount().Difficulty()
					thresholdShareDiff = new(big.Int).Div(new(big.Int).Mul(currentHeaderShareDiff, params.ShareDiffRelativeThreshold), big.NewInt(100))
				}

				if shareDiff.Cmp(thresholdShareDiff) < 0 {
					backend.Logger().WithFields(log.Fields{
						"peer":               id,
						"workshareDiff":      shareDiff,
						"currentHeaderDiff":  currentHeaderShareDiff,
						"shareDiffThreshold": thresholdShareDiff,
					}).Warn("workshare difficulty is too high")
					return pubsub.ValidationReject
				}

				powHashBigInt := new(big.Int).SetBytes(powHash.Bytes())

				// Check if satisfies workShareTarget
				if powHashBigInt.Cmp(workShareTarget) > 0 {
					backend.Logger().WithFields(log.Fields{
						"peer":            id,
						"workShareTarget": workShareTarget,
						"powHash":         powHash.Hex(),
						"powHashBigInt":   powHashBigInt.String(),
					}).Warn("Workshare hash exceeds target")
					return pubsub.ValidationReject
				}

			} else {
				threshold := backend.GetWorkShareP2PThreshold()
				if !backend.CheckWorkThreshold(block.WorkObjectHeader(), threshold) {
					backend.Logger().Error("workshare has less entropy than the workshare p2p threshold")
					return pubsub.ValidationReject
				}

				// After the goldenage fork v3 if a share that is not a workshare is broadcasted without the
				// transactions then throw an error
				isWorkShare := backend.CheckIfValidWorkShare(block.WorkObjectHeader()) == types.Valid
				if !isWorkShare && len(block.WorkObject.Transactions()) == 0 {
					return pubsub.ValidationReject
				}

				powHash, err := backend.ComputePowHash(block.WorkObject.WorkObjectHeader())
				if err != nil {
					backend.Logger().WithField("err", err).Error("Error computing the powHash of the work object header received from peer")
					return pubsub.ValidationReject
				}

				currentHeaderHash := currentHeader.Hash()
				// cannot have a pow filter when the current header is genesis
				if backend.IsGenesisHash(currentHeaderHash) {
					return pubsub.ValidationAccept
				}

				currentHeaderPowHash, err := backend.ComputePowHash(currentHeader.WorkObjectHeader())
				if err != nil {
					return pubsub.ValidationReject
				}
				currentHeaderIntrinsic := common.IntrinsicLogEntropy(currentHeaderPowHash)
				if currentHeaderIntrinsic == nil {
					return pubsub.ValidationIgnore
				}

				workShareIntrinsicEntropy := common.IntrinsicLogEntropy(powHash)
				if workShareIntrinsicEntropy == nil {
					return pubsub.ValidationIgnore
				}

				// Check if the Block is atleast half the current difficulty in Zone Context,
				// this makes sure that the nodes don't listen to the forks with the PowHash
				//	with less than 50% of current difficulty
				if backend.NodeCtx() == common.ZONE_CTX && workShareIntrinsicEntropy.Cmp(new(big.Int).Div(currentHeaderIntrinsic, big.NewInt(2))) < 0 {
					return pubsub.ValidationIgnore
				}
			}
		case *types.AuxTemplate:
			// Make sure no value in the template is nil, unless its specific
			protoAuxTemplate := new(types.ProtoAuxTemplate)
			err := proto.Unmarshal(protoData, protoAuxTemplate)
			if err != nil {
				return pubsub.ValidationReject
			}

			auxTemplate := &types.AuxTemplate{}
			err = auxTemplate.ProtoDecode(protoAuxTemplate)
			if err != nil {
				return pubsub.ValidationReject
			}

			// Verify using the embedded signature bytes (if present)
			if !auxTemplate.VerifySignature() {
				return pubsub.ValidationReject
			}

			// AuxTemplate specific checks

			signatureTime := auxTemplate.SignatureTime()
			backend := *g.consensus.GetBackend(topic.location)
			if backend == nil {
				log.Global.WithFields(log.Fields{
					"peer": id,
				}).Error("no backend found for this location")
			}

			currentTime := uint64(time.Now().Unix())

			// If the signature time is too far in the future, reject the message
			if currentTime+params.AuxTemplateLivenessTime < uint64(signatureTime) {
				return pubsub.ValidationReject
			}
			// If the signature time is too old, reject the message
			if currentTime > uint64(signatureTime)+params.AuxTemplateStaleTime {
				return pubsub.ValidationReject
			}
			// If the signature time is older than the liveness time, ignore the message
			if currentTime > uint64(signatureTime)+params.AuxTemplateLivenessTime {
				return pubsub.ValidationIgnore
			}
		}
		return pubsub.ValidationAccept
	}
}

// unsubscribe from broadcasts of the given type of data
func (g *PubsubManager) Unsubscribe(location common.Location, datatype interface{}) error {
	if topic, err := NewTopic(g.genesis, location, datatype); err == nil {
		if value, ok := g.subscriptions.Load(topic.String()); ok {
			value.(*pubsub.Subscription).Cancel()
			g.subscriptions.Delete(topic.String())
		}
		if value, ok := g.topics.Load(topic.String()); ok {
			value.(*pubsub.Topic).Close()
			g.topics.Delete(topic.String())
		}
		return nil
	} else {
		return err
	}
}

// broadcasts data to subscribing peers
func (g *PubsubManager) Broadcast(location common.Location, data interface{}) error {
	topicName, err := NewTopic(g.genesis, location, data)
	if err != nil {
		return err
	}

	backend := g.consensus.GetBackend(location)
	if backend != nil {
		apiBackend := *backend
		if location.Context() == common.ZONE_CTX && backend != nil && apiBackend.ChainConfig().TelemetryEnabled {
			// If broadcasting a workshare, track it as received (by network)
			if wsv, ok := data.(*types.WorkObjectShareView); ok && wsv != nil && wsv.WorkObject != nil && wsv.WorkObject.WorkObjectHeader() != nil {
				telemetry.RemoveMinedShare(wsv.WorkObject.WorkObjectHeader().Hash())
			}
		}
	}

	protoData, err := pb.ConvertAndMarshal(data)
	if err != nil {
		return err
	}

	// Check if this broadcast has already been sent recently
	broadcastCacheKey := topicName.String() + common.Hash(blake3.Sum256(protoData)).String()
	if _, ok := g.broadcastCache.Get(broadcastCacheKey); ok {
		return nil
	}
	g.broadcastCache.Add(broadcastCacheKey, struct{}{})

	if value, ok := g.topics.Load(topicName.String()); ok {
		return value.(*pubsub.Topic).Publish(g.ctx, protoData)
	}

	return ErrNoTopic
}
