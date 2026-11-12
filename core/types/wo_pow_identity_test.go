package types

import (
	"math/big"
	"strconv"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// shaShareTestHeader returns a SHA workshare header after the KawPow fork. The
// coinbase commits to the seal hash, as a mined share does.
func shaShareTestHeader() *WorkObjectHeader {
	wo, _ := woTestDataWithAuxPow()
	wh := wo.WorkObjectHeader()
	btcHeader := NewBitcoinBlockHeader(1, [32]byte{1}, [32]byte{2}, 1000, 0x1d00ffff, 7)
	coinbaseTx := NewAuxPowCoinbaseTx(SHA_BTC, 100, []byte{0x51}, wh.SealHash(), 123)
	wh.SetAuxPow(NewAuxPow(SHA_BTC, NewAuxPowHeader(btcHeader), []byte{}, []byte{0x01, 0x02}, [][]byte{}, coinbaseTx))
	return wh
}

func protoRoundTripWorkObjectHeader(t *testing.T, wh *WorkObjectHeader) *WorkObjectHeader {
	t.Helper()
	protoWh, err := wh.ProtoEncode()
	require.NoError(t, err)
	data, err := proto.Marshal(protoWh)
	require.NoError(t, err)
	decodedProto := new(ProtoWorkObjectHeader)
	require.NoError(t, proto.Unmarshal(data, decodedProto))
	decoded := new(WorkObjectHeader)
	require.NoError(t, decoded.ProtoDecode(decodedProto, wh.Location()))
	return decoded
}

// A third party can change the AuxPow signature bytes of a share. The work does
// not commit to them, so the PoW hash and the seal hash stay the same, but
// Hash() changes. PowIdentity must stay the same.
func TestPowIdentityIgnoresAuxPowSignature(t *testing.T) {
	original := shaShareTestHeader()
	copied := CopyWorkObjectHeader(original)
	copied.AuxPow().SetSignature([]byte{0xde, 0xad, 0xbe, 0xef})

	require.Equal(t, original.AuxPow().Header().PowHash(), copied.AuxPow().Header().PowHash(), "the work must be the same")
	require.Equal(t, original.SealHash(), copied.SealHash(), "the seal hash must be the same")
	require.NotEqual(t, original.Hash(), copied.Hash(), "Hash() covers the signature, so it differs")
	require.Equal(t, original.PowIdentity(), copied.PowIdentity())
}

// SHA_BTC and SHA_BCH use the same 80 byte header, so one header is one work.
func TestPowIdentityIgnoresShaPowID(t *testing.T) {
	original := shaShareTestHeader()
	copied := CopyWorkObjectHeader(original)
	copied.AuxPow().SetPowID(SHA_BCH)

	require.NotEqual(t, original.Hash(), copied.Hash())
	require.Equal(t, original.PowIdentity(), copied.PowIdentity())
}

// AuxPow2 nil and empty sign the same template, but they encode differently on
// the wire and survive decoding, so Hash() differs. This also applies to shares
// with a valid signature.
func TestPowIdentityIgnoresAuxPow2Encoding(t *testing.T) {
	for _, powID := range []PowID{SHA_BTC, Kawpow} {
		var original *WorkObjectHeader
		if powID == Kawpow {
			wo, _ := woTestDataWithAuxPow()
			original = wo.WorkObjectHeader()
		} else {
			original = shaShareTestHeader()
		}
		original.AuxPow().SetAuxPow2([]byte{})
		withEmpty := protoRoundTripWorkObjectHeader(t, original)

		withNil := CopyWorkObjectHeader(original)
		withNil.AuxPow().SetAuxPow2(nil)
		withNil = protoRoundTripWorkObjectHeader(t, withNil)

		require.Equal(t, withEmpty.AuxPow().ConvertToTemplate().Hash(), withNil.AuxPow().ConvertToTemplate().Hash(), "%v: the signed template must be the same", powID)
		require.NotEqual(t, withEmpty.Hash(), withNil.Hash(), "%v: nil and empty AuxPow2 must hash differently", powID)
		require.Equal(t, withEmpty.PowIdentity(), withNil.PowIdentity(), "%v", powID)
	}
}

func TestNewWorkObjectBodyCommitsCopiedUncles(t *testing.T) {
	for _, powID := range []PowID{SHA_BTC, Kawpow} {
		for _, omitted := range []bool{true, false} {
			t.Run(powID.String()+"/omitted="+strconv.FormatBool(omitted), func(t *testing.T) {
				var uncle *WorkObjectHeader
				if powID == Kawpow {
					wo, _ := woTestDataWithAuxPow()
					uncle = wo.WorkObjectHeader()
				} else {
					uncle = shaShareTestHeader()
				}
				if omitted {
					uncle.AuxPow().SetAuxPow2(nil)
				} else {
					uncle.AuxPow().SetAuxPow2([]byte{})
				}
				uncle = protoRoundTripWorkObjectHeader(t, uncle)
				if omitted {
					require.Nil(t, uncle.AuxPow().AuxPow2(), "protobuf decode must preserve the omitted field")
				}
				body, err := NewWorkObjectBody(EmptyHeader(), nil, nil, []*WorkObjectHeader{uncle}, nil, nil, nil, common.ZONE_CTX)
				require.NoError(t, err)
				if omitted {
					require.NotEqual(t, uncle.Hash(), body.Uncles()[0].Hash(), "the copy must change the omitted-field encoding")
				}
				require.Equal(t, CalcUncleHash(body.Uncles()), body.Header().UncleHash())
			})
		}
	}
}

// Different donor headers are different work, so they must keep different
// identities.
func TestPowIdentityDiffersForDifferentWork(t *testing.T) {
	original := shaShareTestHeader()
	other := CopyWorkObjectHeader(original)
	other.AuxPow().Header().SetNonce(8)

	require.NotEqual(t, original.AuxPow().Header().PowHash(), other.AuxPow().Header().PowHash())
	require.NotEqual(t, original.PowIdentity(), other.PowIdentity())
}

// Before the KawPow fork, Hash() already identifies the work.
func TestPowIdentityBeforeKawPowIsHash(t *testing.T) {
	wo, _ := woTestData()
	wh := wo.WorkObjectHeader()
	wh.SetPrimeTerminusNumber(new(big.Int).SetUint64(params.KawPowForkBlock - 1))
	require.Equal(t, wh.Hash(), wh.PowIdentity())
}
