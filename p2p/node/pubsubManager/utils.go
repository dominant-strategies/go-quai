package pubsubManager

import (
	"errors"
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
	C_workObjectShareType               = "worksharev2"
	C_auxTemplateType                   = "auxtemplate"
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
	case *types.WorkObjectHeaderView:
		return strings.Join([]string{baseTopic, C_headerType}, "/")
	case *types.WorkObjectBlockView, []*types.WorkObjectBlockView:
		return strings.Join([]string{baseTopic, C_workObjectType}, "/")
	case *types.WorkObjectShareView:
		return strings.Join([]string{baseTopic, C_workObjectShareType}, "/")
	case *types.AuxTemplate:
		return strings.Join([]string{baseTopic, C_auxTemplateType}, "/")
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
	case *types.WorkObjectShareView:
		requestDegree = C_defaultRequestDegree
	case *types.WorkObjectHeaderView:
		requestDegree = C_workObjectHeaderTypeRequestDegree
	case *types.WorkObjectBlockView, []*types.WorkObjectBlockView:
		requestDegree = C_workObjectRequestDegree
	case *types.AuxTemplate:
		requestDegree = C_defaultRequestDegree
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

func TopicFromString(topic string) (*Topic, error) {
	topicParts := strings.Split(topic, "/")
	if len(topicParts) < 3 {
		return nil, errors.New("topic string should have 3 parts")
	}
	if len(topicParts[0]) != 66 {
		return nil, errors.New("invalid genesis hash")
	}
	genHash := common.HexToHash(topicParts[0])
	if genHash.String() == "0x0000000000000000000000000000000000000000000000000000000000000000" {
		return nil, errors.New("invalid genesis hash")
	}
	var location common.Location
	if loc := topicParts[1]; loc != "" {
		locationParts := strings.Split(loc, ",")
		if len(locationParts) > 2 {
			return nil, errors.New("invalid location encoding")
		}
		if len(locationParts) > 1 {
			zone, err := strconv.Atoi(locationParts[1])
			if err != nil {
				return nil, err
			}
			if err := location.SetZone(zone); err != nil {
				return nil, err
			}
		}
		if len(locationParts) > 0 {
			region, err := strconv.Atoi(locationParts[0])
			if err != nil {
				return nil, err
			}
			if err := location.SetRegion(region); err != nil {
				return nil, err
			}
		}
	}
	switch topicParts[2] {
	case C_headerType:
		return NewTopic(genHash, location, &types.WorkObjectHeaderView{})
	case C_workObjectType:
		return NewTopic(genHash, location, &types.WorkObjectBlockView{})
	case C_workObjectShareType:
		return NewTopic(genHash, location, &types.WorkObjectShareView{})
	case C_auxTemplateType:
		return NewTopic(genHash, location, &types.AuxTemplate{})
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
