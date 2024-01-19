package pubsubManager

import (
	"strings"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
)

const (
	// Data types for gossipsub topics
	C_blockType       = "blocks"
	C_transactionType = "transactions"
	C_headerType      = "headers"
)

// gets the name of the topic for the given type of data
func TopicName(location common.Location, data interface{}) (string, error) {
	switch data.(type) {
	case *types.Block:
		return strings.Join([]string{location.Name(), C_blockType}, "/"), nil
	default:
		return "", ErrUnsupportedType
	}
}

