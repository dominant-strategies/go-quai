package types

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
)

func TestScryptAuxPow2LengthAtDecode(t *testing.T) {
	powID := uint32(Scrypt)
	for _, size := range []int{0, 1, 31, 33} {
		t.Run(fmt.Sprintf("length_%d", size), func(t *testing.T) {
			payload := bytes.Repeat([]byte{1}, size)
			var template AuxTemplate
			if err := template.ProtoDecode(&ProtoAuxTemplate{ChainId: &powID, AuxPow2: payload}); err == nil {
				t.Fatal("template decoder accepted non-32-byte auxPow2")
			}
			var protoAux AuxPow
			if err := protoAux.ProtoDecode(&ProtoAuxPow{ChainId: &powID, Auxpow2: payload}); err == nil {
				t.Fatal("protobuf decoder accepted non-32-byte auxPow2")
			}
			jsonInput := fmt.Sprintf(`{"powId":"0x%x","header":"0x","auxpow2":"0x%s","signature":"0x","merkleBranch":[],"transaction":"0x"}`, powID, hex.EncodeToString(payload))
			var jsonAux AuxPow
			if err := jsonAux.UnmarshalJSON([]byte(jsonInput)); err == nil || !strings.Contains(err.Error(), "auxPow2") {
				t.Fatalf("JSON decoder did not reject auxPow2 length: %v", err)
			}
		})
	}
	valid := bytes.Repeat([]byte{1}, common.HashLength)
	var template AuxTemplate
	if err := template.ProtoDecode(&ProtoAuxTemplate{ChainId: &powID, AuxPow2: valid}); err != nil {
		t.Fatalf("valid-length Scrypt template rejected: %v", err)
	}
	var aux AuxPow
	if err := aux.ProtoDecode(&ProtoAuxPow{ChainId: &powID, Auxpow2: valid, Header: make([]byte, 80)}); err != nil {
		t.Fatalf("valid-length Scrypt auxPow rejected: %v", err)
	}
}
