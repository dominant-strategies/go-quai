package protocol_test

import (
	"bufio"
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	quaiprotocol "github.com/dominant-strategies/go-quai/p2p/protocol"
)

func TestQuaiProtocolHandler(t *testing.T) {

	// Create a mock network and two hosts
	mockedNetwork := mocknet.New()

	host1, err := mockedNetwork.GenPeer()
	require.NoError(t, err)
	defer host1.Close()

	host2, err := mockedNetwork.GenPeer()
	require.NoError(t, err)
	defer host2.Close()

	// Connect the two hosts on the mock network
	err = mockedNetwork.LinkAll()
	require.NoError(t, err)

	tests := []struct {
		name            string
		ProtocolVersion string
		wantErr         bool
	}{
		{
			name:            "valid protocol",
			ProtocolVersion: quaiprotocol.ProtocolVersion,
			wantErr:         false,
		},
		{
			name:            "invalid protocol",
			ProtocolVersion: "invalid",
			wantErr:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Convert string to protocol.ID type
			protocolID := protocol.ID(tt.ProtocolVersion)

			// Register protocol handler on host2
			host2.SetStreamHandler(protocolID, quaiprotocol.QuaiProtocolHandler)

			// Establish a stream from host1 (sender) to host2 (receiver)
			stream, err := host1.NewStream(ctx, peer.ID(host2.ID()), protocolID)
			assert.NoError(t, err)
			defer stream.Close()

			// Send a message to host2
			_, err = stream.Write([]byte("hello\n"))
			assert.NoError(t, err)

			// Read the response from host2
			buf := bufio.NewReader(stream)
			response, err := buf.ReadString('\n')

			if tt.wantErr {
				// Assert the stream is closed
				assert.Error(t, err)
			} else {
				// Assert that the response is as expected
				assert.NoError(t, err)
				expectedResponse := "Hello from the other side!\n"
				assert.Equal(t, expectedResponse, response)
			}
		})
	}

}
