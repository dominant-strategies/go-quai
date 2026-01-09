package pubsubManager

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/dominant-strategies/go-quai/cmd/utils"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	mock_p2p "github.com/dominant-strategies/go-quai/p2p/mocks"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quai"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*mock_p2p.MockHost, *mock_p2p.MockPeerstore, crypto.PrivKey, peer.ID) {
	ctrl := gomock.NewController(t)

	viper.Set(utils.EnvironmentFlag.Name, params.LocalName)
	// Set a valid genesis nonce to avoid "Genesis nonce is too short" error
	viper.Set(utils.GenesisNonce.Name, "0x0123456789abcdef0123456789abcdef")

	privKey, pubKey, err := crypto.GenerateKeyPair(crypto.Secp256k1, 256)
	if err != nil {
		t.Fatalf("Failed to generate node key: %v", err)
	}
	peerID, err := peer.IDFromPublicKey(pubKey)
	if err != nil {
		t.Fatalf("Failed to generate peer ID: %v", err)
	}
	mockHost := mock_p2p.NewMockHost(ctrl)
	mockPeerStore := mock_p2p.NewMockPeerstore(ctrl)
	mockHost.EXPECT().ConnManager().Return(nil).AnyTimes()
	mockHost.EXPECT().ID().Return(peerID).AnyTimes()
	mockHost.EXPECT().Peerstore().Return(mockPeerStore).AnyTimes()
	mockNetwork := mock_p2p.NewMockNetwork(ctrl)
	mockNetwork.EXPECT().Notify(gomock.Any()).Return().AnyTimes()
	mockNetwork.EXPECT().Peers().Return([]peer.ID{peerID}).AnyTimes()
	mockNetwork.EXPECT().ConnsToPeer(peerID).Return(nil).AnyTimes()

	mockHost.EXPECT().SetStreamHandler(gomock.Any(), gomock.Any()).Return().AnyTimes()
	mockHost.EXPECT().Network().Return(mockNetwork).AnyTimes()

	return mockHost, mockPeerStore, privKey, peerID
}

func TestPubsubManager(t *testing.T) {
	ctx := context.Background()

	mockHost, mockPeerStore, privKey, peerID := setup(t)

	t.Run("NewGossipSubManager Error case", func(t *testing.T) {
		// Force libp2p to return an error (no private key)
		mockPeerStore.EXPECT().PrivKey(peerID).Return(nil).Times(1)
		ps, err := NewGossipSubManager(ctx, mockHost)
		require.Nil(t, ps)
		require.Error(t, err)
	})

	// Success case
	mockPeerStore.EXPECT().PrivKey(peerID).Return(privKey).AnyTimes()

	ps, err := NewGossipSubManager(ctx, mockHost)
	require.NoError(t, err, "Failed to create gossipsub manager")

	validatorFunc := func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
		return pubsub.ValidationAccept
	}

	t.Run("Subscribe - New topic error", func(t *testing.T) {
		err = ps.SubscribeAndRegisterValidator(common.Location{0, 0}, "Wrong data type", validatorFunc)
		require.Error(t, err, "Expected error on wrong data type")
	})

	t.Run("Subscribe - QuaiBackend not set error", func(t *testing.T) {
		err = ps.SubscribeAndRegisterValidator(common.Location{0, 0}, &types.WorkObjectBlockView{}, validatorFunc)
		require.ErrorIs(t, err, ErrConsensusNotSet)
	})

	quaiBackend, _ := quai.NewQuaiBackend()
	ps.SetQuaiBackend(quaiBackend)
	t.Run("Subscribe", func(t *testing.T) {
		if len(ps.GetTopics()) != 0 {
			t.Fatal("Topic should be empty before subscription")
		}
		err = ps.SubscribeAndRegisterValidator(common.Location{0, 0}, &types.WorkObjectBlockView{}, validatorFunc)
		require.NoError(t, err, "Failed to subscribe to topic")
		if entry := len(ps.GetTopics()); entry != 1 {
			t.Fatalf("Expected 1 topic, got %d", entry)
		}
		// Can't Subscribe to same topic
		err = ps.SubscribeAndRegisterValidator(common.Location{0, 0}, &types.WorkObjectBlockView{}, validatorFunc)
		require.Error(t, err, "Expected error on subscribe to same topic")
	})

	t.Run("Broadcast", func(t *testing.T) {
		// Set the receive handler function
		testCh := make(chan interface{})
		ps.SetReceiveHandler(func(receivedFrom peer.ID, msgId string, msgTopic string, data interface{}, location common.Location) {
			testCh <- data
		})

		// Create and broadcast a new WorkObjectBlock
		newWo := types.EmptyZoneWorkObject()
		broadcastedMessage := newWo.ConvertToBlockView()
		err = ps.Broadcast(common.Location{0, 0}, broadcastedMessage)
		require.NoError(t, err, "Failed to broadcast message")

		// Verify if subscription received correct message type
		receivedMessage := <-testCh
		recvdWorkObject, ok := receivedMessage.(types.WorkObjectBlockView)
		require.True(t, ok, "Unable to cast workobject")

		// Verify equality of the send and receive
		require.Equal(t, broadcastedMessage.Hash(), recvdWorkObject.Hash())
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		err = ps.Unsubscribe(common.Location{0, 0}, "Wrong data type")
		require.Error(t, err, "Shouldn't unsubscribe from wrong data type")

		err = ps.Unsubscribe(common.Location{0, 0}, &types.WorkObjectBlockView{})
		require.NoError(t, err, "Failed to unsubscribe from topic")

		if len(ps.GetTopics()) != 0 {
			t.Fatal("Topic should be empty after unsubscribe")
		}
		err = ps.Broadcast(common.Location{0, 0}, common.Hash{2})
		require.Error(t, err, "Should not broadcast to unsubscribed topic")
	})

	ps.Stop()
}

func TestMultipleRequests(t *testing.T) {
	// Number of requests to test
	n := 100

	// Use a context with timeout to avoid hanging indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	mockHost, mockPeerStore, privKey, peerID := setup(t)

	mockPeerStore.EXPECT().PrivKey(peerID).Return(privKey).AnyTimes()

	ps, err := NewGossipSubManager(ctx, mockHost)
	require.NoError(t, err, "Failed to create gossipsub manager")

	quaiBackend, _ := quai.NewQuaiBackend()
	ps.SetQuaiBackend(quaiBackend)

	wo := types.EmptyZoneWorkObject()

	tx := types.NewEmptyQuaiTx()
	txs := types.Transactions{tx}

	headerView := wo.ConvertToHeaderView()
	blockView := wo.ConvertToBlockView()
	workShareView := wo.ConvertToWorkObjectShareView(txs)

	var topics []interface{}
	topics = append(topics, headerView)
	topics = append(topics, blockView)
	topics = append(topics, workShareView)

	validatorFunc := func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
		return pubsub.ValidationAccept
	}

	// SUBSCRIBE to all topics
	for i, topic := range topics {
		err := ps.SubscribeAndRegisterValidator(common.Location{0, 0}, topic, validatorFunc)
		require.NoError(t, err, "Failed to subscribe to topic %d", topic)
		if entry := len(ps.GetTopics()); entry != i+1 {
			t.Fatalf("Expected %d topic, got %d", (i + 1), entry)
		}
	}

	// Create a buffered channel large enough to hold all sent messages
	testCh := make(chan interface{}, n*len(topics))
	ps.SetReceiveHandler(func(receivedFrom peer.ID, msgId string, msgTopic string, data interface{}, location common.Location) {
		select {
		case testCh <- data:
		case <-ctx.Done():
			t.Error("context done before send messageId: ", msgId)
		}
	})

	var messages []interface{}
	var wg sync.WaitGroup

	// BROADCAST messages concurrently
	for i := 0; i < n; i++ {
		newWo := types.CopyWorkObject(wo)
		newWo.WorkObjectHeader().SetNonce(types.EncodeNonce(uint64(i)))
		for _, topic := range topics {
			var msg interface{}
			switch topic.(type) {
			case *types.WorkObjectHeaderView:
				msg = newWo.ConvertToHeaderView()
			case *types.WorkObjectBlockView:
				msg = newWo.ConvertToBlockView()
			case *types.WorkObjectShareView:
				msg = newWo.ConvertToWorkObjectShareView(txs)
			}

			messages = append(messages, msg)
			wg.Add(1)
			// Add a sleep to not overwhelm the gossipSub broadcasts
			time.Sleep(10 * time.Millisecond)

			// Broadcast each message in its own goroutine
			go func(msg interface{}) {
				defer wg.Done()
				err := ps.Broadcast(common.Location{0, 0}, msg)
				require.NoError(t, err, "Failed to broadcast message")
			}(msg)
		}
	}

	// VERIFY receiving concurrently
	receivedMessages := make([]interface{}, 0, n*len(topics))

	// Wait for all broadcasts to complete
	wg.Wait()

	// Read all messages from the channel
loop:
	for i := 0; i < n*len(topics); i++ {
		select {
		case receivedMessage := <-testCh:
			receivedMessages = append(receivedMessages, receivedMessage)
		case <-ctx.Done():
			// If context is done, we stop reading and let the assertion fail
			break loop
		}
	}

	// Ensure all broadcasted messages were received
	require.Len(t, receivedMessages, len(messages), "The number of received messages does not match the number of broadcasted messages. sent: %d, received: %d", len(messages), len(receivedMessages))

	ps.Stop()
	if len(ps.GetTopics()) != 0 {
		t.Fatal("Topic should be empty after unsubscribe")
	}
}
