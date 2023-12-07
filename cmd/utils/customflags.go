package utils

import (
	"encoding"
	"fmt"
	"math/big"
)

// GenericFlag encapsulates common attributes and an interface for value
type Flag struct {
	Name         string
	Abbreviation string
	Value        interface{}
	Usage        string
}

// Implementing the Flag interface
func (f *Flag) GetName() string         { return f.Name }
func (f *Flag) GetAbbreviation() string { return f.Abbreviation }
func (f *Flag) GetUsage() string        { return f.Usage }
func (f *Flag) GetValue() interface{}   { return f.Value }

// ****************************************
// **                                    **
// **       BIG INT FLAG                 **
// **       & CUSTOM VALUE               **
// **                                    **
// ****************************************
type BigIntValue big.Int

func newBigIntValue(val *big.Int) *BigIntValue {
	if val == nil {
		return nil
	}
	return (*BigIntValue)(val)
}

func (b *BigIntValue) Set(val string) error {
	bigIntVal, ok := new(big.Int).SetString(val, 10)
	if !ok {
		return fmt.Errorf("failed to parse *big.Int value: %s", val)
	}
	*b = BigIntValue(*bigIntVal)
	return nil
}

func (b *BigIntValue) Type() string {
	return "big.Int"
}

func (b *BigIntValue) String() string {
	return (*big.Int)(b).String()
}

// ****************************************
// **                                    **
// **       TEXT MARSHALER FLAG          **
// **       & CUSTOM VALUE               **
// **                                    **
// ****************************************
type TextMarshaler interface {
	encoding.TextMarshaler
	encoding.TextUnmarshaler
}

type TextMarshalerValue struct {
	Value encoding.TextMarshaler
}

func NewTextMarshalerValue(val encoding.TextMarshaler) *TextMarshalerValue {
	return &TextMarshalerValue{Value: val}
}

func (t *TextMarshalerValue) Set(val string) error {
	if unmarshaler, ok := t.Value.(encoding.TextUnmarshaler); ok {
		return unmarshaler.UnmarshalText([]byte(val))
	}
	return fmt.Errorf("value does not implement encoding.TextUnmarshaler")
}

func (t *TextMarshalerValue) Type() string {
	return "textMarshaler"
}

func (t *TextMarshalerValue) String() string {
	text, err := t.Value.MarshalText()
	if err != nil {
		return ""
	}
	return string(text)
}
