package pubsubManager

import (
	"bytes"
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

// setupAuxTemplateTest sets up the test environment for AuxTemplate tests
func setupAuxTemplateTest(t *testing.T) (*mock_p2p.MockHost, *mock_p2p.MockPeerstore, crypto.PrivKey, peer.ID) {
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

// createTestAuxTemplate creates a test AuxTemplate with sample data
func createTestAuxTemplate(nonce uint64) *types.AuxTemplate {
	var prevHash [32]byte
	copy(prevHash[:], bytes.Repeat([]byte{byte(nonce % 256)}, 32))

	coinbaseOut := []byte{0x76, 0xa9, 0x14, byte(nonce)}

	template := &types.AuxTemplate{}
	template.SetPowID(types.PowID(1000 + nonce))
	template.SetPrevHash(prevHash)
	template.SetCoinbaseOut(coinbaseOut)
	template.SetVersion(0x20000000)
	template.SetNBits(0x1d00ffff)
	template.SetSignatureTime(0xffffffff)
	template.SetHeight(uint32(12345 + nonce))

	if nonce%2 != 0 { // If not coinbase-only, add merkle branch
		template.SetMerkleBranch([][]byte{
			bytes.Repeat([]byte{0xaa}, 32),
			bytes.Repeat([]byte{0xbb}, 32),
		})
	} else {
		template.SetMerkleBranch(nil)
	}

	template.SetSigs(make([]byte, 64)) // Dummy signature

	return template
}

// TestAuxTemplatePubsubManager tests the pubsub manager with AuxTemplate
func TestAuxTemplatePubsubManager(t *testing.T) {
	ctx := context.Background()

	mockHost, mockPeerStore, privKey, peerID := setupAuxTemplateTest(t)

	// Success case
	mockPeerStore.EXPECT().PrivKey(peerID).Return(privKey).AnyTimes()

	ps, err := NewGossipSubManager(ctx, mockHost)
	require.NoError(t, err, "Failed to create gossipsub manager")

	validatorFunc := func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
		return pubsub.ValidationAccept
	}

	quaiBackend, _ := quai.NewQuaiBackend()
	ps.SetQuaiBackend(quaiBackend)

	t.Run("Subscribe to AuxTemplate topic", func(t *testing.T) {
		if len(ps.GetTopics()) != 0 {
			t.Fatal("Topic should be empty before subscription")
		}

		// Subscribe to AuxTemplate topic
		err = ps.SubscribeAndRegisterValidator(common.Location{0, 0}, &types.AuxTemplate{}, validatorFunc)
		require.NoError(t, err, "Failed to subscribe to AuxTemplate topic")

		if entry := len(ps.GetTopics()); entry != 1 {
			t.Fatalf("Expected 1 topic, got %d", entry)
		}

		// Can't Subscribe to same topic
		err = ps.SubscribeAndRegisterValidator(common.Location{0, 0}, &types.AuxTemplate{}, validatorFunc)
		require.Error(t, err, "Expected error on subscribe to same topic")
	})

	t.Run("Broadcast AuxTemplate", func(t *testing.T) {
		// Set the receive handler function
		testCh := make(chan interface{})
		ps.SetReceiveHandler(func(receivedFrom peer.ID, msgId string, msgTopic string, data interface{}, location common.Location) {
			testCh <- data
		})

		// Create and broadcast a new AuxTemplate
		auxTemplate := createTestAuxTemplate(1)
		err = ps.Broadcast(common.Location{0, 0}, auxTemplate)
		require.NoError(t, err, "Failed to broadcast AuxTemplate")

		// Verify if subscription received correct message type
		receivedMessage := <-testCh
		recvdAuxTemplate, ok := receivedMessage.(*types.AuxTemplate)
		require.True(t, ok, "Unable to cast to AuxTemplate")

		// Verify equality of the sent and received templates
		require.Equal(t, auxTemplate.PowID(), recvdAuxTemplate.PowID())
		require.Equal(t, auxTemplate.PrevHash(), recvdAuxTemplate.PrevHash())
		require.Equal(t, auxTemplate.CoinbaseOut(), recvdAuxTemplate.CoinbaseOut())
		require.Equal(t, auxTemplate.Height(), recvdAuxTemplate.Height())
	})

	t.Run("Unsubscribe from AuxTemplate", func(t *testing.T) {
		err = ps.Unsubscribe(common.Location{0, 0}, &types.AuxTemplate{})
		require.NoError(t, err, "Failed to unsubscribe from AuxTemplate topic")

		if len(ps.GetTopics()) != 0 {
			t.Fatal("Topic should be empty after unsubscribe")
		}

		// Should not be able to broadcast after unsubscribe
		auxTemplate := createTestAuxTemplate(2)
		err = ps.Broadcast(common.Location{0, 0}, auxTemplate)
		require.Error(t, err, "Should not broadcast to unsubscribed topic")
	})

	ps.Stop()
}

// TestMultipleAuxTemplateRequests tests multiple concurrent AuxTemplate broadcasts
func TestMultipleAuxTemplateRequests(t *testing.T) {
	// Number of requests to test
	n := 50 // Reduced from 100 for AuxTemplate since it's a larger message

	// Use a context with timeout to avoid hanging indefinitely
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	mockHost, mockPeerStore, privKey, peerID := setupAuxTemplateTest(t)

	mockPeerStore.EXPECT().PrivKey(peerID).Return(privKey).AnyTimes()

	ps, err := NewGossipSubManager(ctx, mockHost)
	require.NoError(t, err, "Failed to create gossipsub manager")

	quaiBackend, _ := quai.NewQuaiBackend()
	ps.SetQuaiBackend(quaiBackend)

	validatorFunc := func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
		return pubsub.ValidationAccept
	}

	// Subscribe to AuxTemplate topic at different locations
	locations := []common.Location{
		{0, 0}, // Zone 0,0
		{0, 1}, // Zone 0,1
		{1, 0}, // Zone 1,0
	}

	for _, loc := range locations {
		err := ps.SubscribeAndRegisterValidator(loc, &types.AuxTemplate{}, validatorFunc)
		require.NoError(t, err, "Failed to subscribe to AuxTemplate topic at location %v", loc)
	}

	// Create a buffered channel large enough to hold all sent messages
	testCh := make(chan interface{}, n*len(locations))
	receivedLocations := make(map[string][]common.Location)
	var mu sync.Mutex

	ps.SetReceiveHandler(func(receivedFrom peer.ID, msgId string, msgTopic string, data interface{}, location common.Location) {
		select {
		case testCh <- data:
			mu.Lock()
			if _, ok := data.(*types.AuxTemplate); ok {
				receivedLocations[msgId] = append(receivedLocations[msgId], location)
			}
			mu.Unlock()
		case <-ctx.Done():
			t.Error("context done before send messageId: ", msgId)
		}
	})

	var messages []*types.AuxTemplate
	var wg sync.WaitGroup

	// BROADCAST messages concurrently
	for i := 0; i < n; i++ {
		for _, loc := range locations {
			auxTemplate := createTestAuxTemplate(uint64(i))
			messages = append(messages, auxTemplate)

			wg.Add(1)
			// Add a sleep to not overwhelm the gossipSub broadcasts
			time.Sleep(10 * time.Millisecond)

			// Broadcast each message in its own goroutine
			go func(template *types.AuxTemplate, location common.Location) {
				defer wg.Done()
				err := ps.Broadcast(location, template)
				require.NoError(t, err, "Failed to broadcast AuxTemplate at location %v", location)
			}(auxTemplate, loc)
		}
	}

	// VERIFY receiving concurrently
	receivedMessages := make([]*types.AuxTemplate, 0, n*len(locations))

	for i := 0; i < (n * len(locations)); i++ {
		wg.Add(1)
		go func(j int) {
			defer wg.Done()
			select {
			case receivedMessage := <-testCh:
				if auxTemplate, ok := receivedMessage.(*types.AuxTemplate); ok {
					mu.Lock()
					receivedMessages = append(receivedMessages, auxTemplate)
					mu.Unlock()
				}
			case <-ctx.Done():
				t.Error("context done before receive message at index: ", j)
			}
		}(i)
	}

	// Wait for all broadcasts to complete
	wg.Wait()

	// Ensure all broadcasted messages were received
	require.Len(t, receivedMessages, len(messages),
		"The number of received messages does not match the number of broadcasted messages. sent: %d, received: %d",
		len(messages), len(receivedMessages))

	// Unsubscribe from all locations
	for _, loc := range locations {
		err = ps.Unsubscribe(loc, &types.AuxTemplate{})
		require.NoError(t, err, "Failed to unsubscribe from location %v", loc)
	}

	ps.Stop()
	if len(ps.GetTopics()) != 0 {
		t.Fatal("Topic should be empty after unsubscribe")
	}
}

// TestAuxTemplateWithMixedMessageTypes tests AuxTemplate alongside other message types
func TestAuxTemplateWithMixedMessageTypes(t *testing.T) {
	ctx := context.Background()

	mockHost, mockPeerStore, privKey, peerID := setupAuxTemplateTest(t)
	mockPeerStore.EXPECT().PrivKey(peerID).Return(privKey).AnyTimes()

	ps, err := NewGossipSubManager(ctx, mockHost)
	require.NoError(t, err, "Failed to create gossipsub manager")

	quaiBackend, _ := quai.NewQuaiBackend()
	ps.SetQuaiBackend(quaiBackend)

	validatorFunc := func(context.Context, peer.ID, *pubsub.Message) pubsub.ValidationResult {
		return pubsub.ValidationAccept
	}

	location := common.Location{0, 0}

	// Subscribe to multiple message types including AuxTemplate
	wo := types.EmptyZoneWorkObject()
	headerView := wo.ConvertToHeaderView()
	blockView := wo.ConvertToBlockView()
	auxTemplate := createTestAuxTemplate(1)

	// Subscribe to all message types
	err = ps.SubscribeAndRegisterValidator(location, headerView, validatorFunc)
	require.NoError(t, err, "Failed to subscribe to header view")

	err = ps.SubscribeAndRegisterValidator(location, blockView, validatorFunc)
	require.NoError(t, err, "Failed to subscribe to block view")

	err = ps.SubscribeAndRegisterValidator(location, auxTemplate, validatorFunc)
	require.NoError(t, err, "Failed to subscribe to AuxTemplate")

	require.Equal(t, 3, len(ps.GetTopics()), "Expected 3 topics")

	// Set up receiver
	receivedMessages := make(map[string]int) // Track message types received
	var mu sync.Mutex

	ps.SetReceiveHandler(func(receivedFrom peer.ID, msgId string, msgTopic string, data interface{}, loc common.Location) {
		mu.Lock()
		defer mu.Unlock()

		switch v := data.(type) {
		case *types.WorkObjectHeaderView:
			receivedMessages["header"]++
		case *types.WorkObjectBlockView:
			receivedMessages["block"]++
		case *types.AuxTemplate:
			receivedMessages["auxtemplate"]++
		case types.WorkObjectHeaderView:
			receivedMessages["header"]++
		case types.WorkObjectBlockView:
			receivedMessages["block"]++
		case types.AuxTemplate:
			receivedMessages["auxtemplate"]++
		default:
			t.Logf("Received unexpected type: %T", v)
		}
	})

	// Broadcast each message type
	err = ps.Broadcast(location, wo.ConvertToHeaderView())
	require.NoError(t, err, "Failed to broadcast header")

	err = ps.Broadcast(location, wo.ConvertToBlockView())
	require.NoError(t, err, "Failed to broadcast block")

	err = ps.Broadcast(location, createTestAuxTemplate(2))
	require.NoError(t, err, "Failed to broadcast AuxTemplate")

	// Wait for messages to be received
	time.Sleep(500 * time.Millisecond)

	// Verify all message types were received
	mu.Lock()
	require.Equal(t, 1, receivedMessages["header"], "Should receive 1 header")
	require.Equal(t, 1, receivedMessages["block"], "Should receive 1 block")
	require.Equal(t, 1, receivedMessages["auxtemplate"], "Should receive 1 AuxTemplate")
	mu.Unlock()

	ps.Stop()
}

// TestAuxTemplateValidation tests AuxTemplate message validation
func TestAuxTemplateValidation(t *testing.T) {
	ctx := context.Background()

	mockHost, mockPeerStore, privKey, peerID := setupAuxTemplateTest(t)
	mockPeerStore.EXPECT().PrivKey(peerID).Return(privKey).AnyTimes()

	ps, err := NewGossipSubManager(ctx, mockHost)
	require.NoError(t, err, "Failed to create gossipsub manager")

	quaiBackend, _ := quai.NewQuaiBackend()
	ps.SetQuaiBackend(quaiBackend)

	location := common.Location{0, 0}

	// Track validation calls
	validationCalls := 0
	validatorFunc := func(ctx context.Context, pid peer.ID, msg *pubsub.Message) pubsub.ValidationResult {
		validationCalls++

		// Could add actual AuxTemplate validation here
		// For example, check if ChainID is valid, signatures are present, etc.

		return pubsub.ValidationAccept
	}

	// Subscribe with validator
	err = ps.SubscribeAndRegisterValidator(location, &types.AuxTemplate{}, validatorFunc)
	require.NoError(t, err, "Failed to subscribe with validator")

	// Set up receiver
	receivedCount := 0
	ps.SetReceiveHandler(func(receivedFrom peer.ID, msgId string, msgTopic string, data interface{}, loc common.Location) {
		if _, ok := data.(*types.AuxTemplate); ok {
			receivedCount++
		}
	})

	// Broadcast multiple AuxTemplates
	for i := 0; i < 5; i++ {
		auxTemplate := createTestAuxTemplate(uint64(i))
		err = ps.Broadcast(location, auxTemplate)
		require.NoError(t, err, "Failed to broadcast AuxTemplate %d", i)
	}

	// Wait for messages to be processed
	time.Sleep(200 * time.Millisecond)

	// Verify validation was called for each message
	require.Equal(t, 5, validationCalls, "Validator should be called 5 times")
	require.Equal(t, 5, receivedCount, "Should receive 5 AuxTemplates")

	ps.Stop()
}
