package types

import (
	"errors"
	"math/big"
)

type TokenChoices struct {
	Quai uint
	Qi   uint
	Diff *big.Int
}

const (
	C_tokenChoiceSetSize = 100
)

type TokenChoiceSet [C_tokenChoiceSetSize][]TokenChoices

func NewTokenChoiceSet() TokenChoiceSet {
	return [C_tokenChoiceSetSize][]TokenChoices{}
}

func (tcs *TokenChoiceSet) ProtoEncode() (*ProtoTokenChoiceSet, error) {
	if tcs == nil {
		return nil, errors.New("TokenChoiceSet is nil")
	}

	protoSet := &ProtoTokenChoiceSet{}

	for _, choices := range tcs {
		protoArray := &ProtoTokenChoiceArray{}

		for _, choice := range choices {
			protoChoice := &ProtoTokenChoice{
				Quai: uint64(choice.Quai),
				Qi:   uint64(choice.Qi),
				Diff: choice.Diff.Bytes(),
			}
			protoArray.TokenChoices = append(protoArray.TokenChoices, protoChoice)
		}

		protoSet.TokenChoiceArray = append(protoSet.TokenChoiceArray, protoArray)
	}

	return protoSet, nil
}

func (tcs *TokenChoiceSet) ProtoDecode(protoSet *ProtoTokenChoiceSet) error {
	if protoSet == nil {
		return errors.New("ProtoTokenChoiceSet is nil")
	}

	for i, protoArray := range protoSet.TokenChoiceArray {
		tcs[i] = make([]TokenChoices, 0, len(protoArray.TokenChoices))

		for _, protoChoice := range protoArray.TokenChoices {
			choice := TokenChoices{
				Quai: uint(protoChoice.Quai),
				Qi:   uint(protoChoice.Qi),
				Diff: new(big.Int).SetBytes(protoChoice.Diff), // Convert bytes back to *big.Int
			}

			tcs[i] = append(tcs[i], choice)
		}
	}

	return nil
}
