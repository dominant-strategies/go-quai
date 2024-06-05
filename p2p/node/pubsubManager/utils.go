package pubsubManager

import (
	"errors"
	"math/big"
	"strconv"
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/ipfs/go-cid"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multihash"
)

const (
	// Data types for gossipsub topics
	C_workObjectType                    = "blocks"
	C_transactionType                   = "transactions"
	C_headerType                        = "headers"
	C_workObjectShareType               = "workshare"
	C_workObjectRequestDegree           = 3
	C_workObjectHeaderTypeRequestDegree = 3
	C_defaultRequestDegree              = 3
)

type Topic struct {
	genesis       common.Hash
	location      common.Location
	data          interface{}
	requestDegree int
	string
}

// gets the name of the topic for the given type of data
func (t *Topic) buildTopicString() string {
	var parts []string
	for _, b := range t.location {
		parts = append(parts, strconv.Itoa(int(b)))
	}
	encodedLocation := strings.Join(parts, ",")
	baseTopic := strings.Join([]string{t.genesis.String(), encodedLocation}, "/")
	switch t.data.(type) {
	case *types.WorkObjectHeaderView, *big.Int, common.Hash:
		return strings.Join([]string{baseTopic, C_headerType}, "/")
	case *types.WorkObjectBlockView:
		return strings.Join([]string{baseTopic, C_workObjectType}, "/")
	case *types.WorkObjectShareView:
		return strings.Join([]string{baseTopic, C_workObjectShareType}, "/")
	default:
		panic(ErrUnsupportedType)
	}
}

func (t *Topic) String() string {
	return t.string
}

func (t *Topic) GetLocation() common.Location {
	return t.location
}

func (t *Topic) GetTopicType() interface{} {
	return t.data
}

func (t *Topic) GetRequestDegree() int {
	return t.requestDegree
}

// gets the name of the topic for the given type of data
func NewTopic(genesis common.Hash, location common.Location, data interface{}) (*Topic, error) {
	var requestDegree int
	switch data.(type) {
	case *types.WorkObjectShareView, common.Hash:
		requestDegree = C_defaultRequestDegree
	case *types.WorkObjectHeaderView:
		requestDegree = C_workObjectHeaderTypeRequestDegree
	case *types.WorkObjectBlockView:
		requestDegree = C_workObjectRequestDegree
	default:
		return nil, ErrUnsupportedType
	}
	t := &Topic{
		genesis:       genesis,
		location:      location,
		data:          data,
		requestDegree: requestDegree,
	}
	t.string = t.buildTopicString()
	return t, nil
}

func TopicFromString(genesis common.Hash, topic string) (*Topic, error) {
	topicParts := strings.Split(topic, "/")
	if len(topicParts) < 3 {
		return nil, ErrMalformedTopic
	}
	var location common.Location
	locationStr := strings.Split(topicParts[1], ",")
	if len(locationStr) > 0 {
		if len(locationStr) >= 1 && locationStr[0] != "" {
			// Region specified
			region, err := strconv.Atoi(locationStr[0])
			if err != nil {
				return nil, err
			}
			location.SetRegion(region)
		}
		if len(locationStr) == 2 && locationStr[1] != "" {
			// Zone specified
			zone, err := strconv.Atoi(locationStr[1])
			if err != nil {
				return nil, err
			}
			location.SetZone(zone)
		}
	}

	switch topicParts[2] {
	case C_headerType:
		return NewTopic(genesis, location, &types.WorkObjectHeaderView{})
	case C_workObjectType:
		return NewTopic(genesis, location, &types.WorkObjectBlockView{})
	case C_workObjectShareType:
		return NewTopic(genesis, location, &types.WorkObjectShareView{})
	default:
		return nil, ErrUnsupportedType
	}
}

// lists our peers which provide the associated topic
func (g *PubsubManager) PeersForTopic(t *Topic) ([]peer.ID, error) {
	if value, ok := g.topics.Load(t.string); ok {
		return value.(*pubsub.Topic).ListPeers(), nil
	}
	return nil, errors.New("no topic for requested data")
}

// Creates a Cid from a location to be used as DHT key
func TopicToCid(topic *Topic) cid.Cid {
	sliceBytes := []byte(topic.string)

	// create a multihash from the slice ID
	mhash, _ := multihash.Encode(sliceBytes, multihash.SHA2_256)

	// create a Cid from the multihash
	return cid.NewCidV1(cid.Raw, mhash)
}
