package types

import (
	"testing"

	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
)

func TestTokenChoiceSetProtoDecodeRejectsMalformedInput(t *testing.T) {
	var choices TokenChoiceSet
	require.Error(t, choices.ProtoDecode(nil))
	require.Error(t, choices.ProtoDecode(&ProtoTokenChoiceSet{
		TokenChoiceArray: []*ProtoTokenChoiceArray{nil},
	}))
	require.Error(t, choices.ProtoDecode(&ProtoTokenChoiceSet{
		TokenChoiceArray: []*ProtoTokenChoiceArray{{}},
	}))

	tooMany := make([]*ProtoTokenChoiceArray, params.TokenChoiceSetSize+1)
	for i := range tooMany {
		tooMany[i] = &ProtoTokenChoiceArray{TokenChoices: &ProtoTokenChoice{}}
	}
	require.Error(t, choices.ProtoDecode(&ProtoTokenChoiceSet{TokenChoiceArray: tooMany}))
}

func TestBetasProtoDecodeRejectsNil(t *testing.T) {
	var betas Betas
	require.Error(t, betas.ProtoDecode(nil))
}
