package pubsubManager

import (
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/libp2p/go-libp2p/core/peer"
)

const (
	// Data types for gossipsub topics
	C_blockType       = "blocks"
	C_transactionType = "transactions"
	C_headerType      = "headers"
	C_hashType        = "hash"
)

// gets the name of the topic for the given type of data
func TopicName(location common.Location, data interface{}) (string, error) {
	switch data.(type) {
	case *types.Block:
		return strings.Join([]string{location.Name(), C_blockType}, "/"), nil
	case common.Hash:
		return strings.Join([]string{location.Name(), C_hashType}, "/"), nil
	default:
		return "", ErrUnsupportedType
	}
}

func getTopicType(topic string) string {
	return topic[strings.LastIndex(topic, "/")+1:]
}

// lists our peers which provide the associated topic
func (g *PubsubManager) PeersForTopic(location common.Location, data interface{}) ([]peer.ID, error) {
	topicName, err := TopicName(location, data)
	if err != nil {
		return nil, err
	}
	return g.topics[topicName].ListPeers(), nil
}
