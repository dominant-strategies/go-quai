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

// Betas struct holds the beta0 and beta1 of the logistic regression for each
// prime block
type Betas struct {
	beta0 *big.Float
	beta1 *big.Float
}

func NewBetas(beta0, beta1 *big.Float) *Betas {
	return &Betas{
		beta0: beta0,
		beta1: beta1,
	}
}

func (b *Betas) Beta0() *big.Float {
	return b.beta0
}

func (b *Betas) Beta1() *big.Float {
	return b.beta1
}

func (b *Betas) ProtoEncode() (*ProtoBetas, error) {
	beta0Bytes, err := b.beta0.GobEncode()
	if err != nil {
		return nil, err
	}
	beta1Bytes, err := b.beta1.GobEncode()
	if err != nil {
		return nil, err
	}
	return &ProtoBetas{
		Beta0: beta0Bytes,
		Beta1: beta1Bytes,
	}, nil
}

func (b *Betas) ProtoDecode(betas *ProtoBetas) error {
	beta0 := new(big.Float).SetInt64(0)
	beta1 := new(big.Float).SetInt64(0)
	err := beta0.GobDecode(betas.GetBeta0())
	if err != nil {
		return err
	}
	err = beta1.GobDecode(betas.GetBeta1())
	if err != nil {
		return err
	}
	// update the beta0 and beta1
	b.beta0 = beta0
	b.beta1 = beta1
	return nil
}
