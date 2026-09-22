package hdwallet

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	btcmusig2 "github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
)

// QiMuSigNonceParam is the wire-friendly nonce bundle entry used by the iOS
// FFI. Signer positions are always final TxIn indexes, which is the same order
// the node uses when it aggregates Qi input keys during signature validation.
type QiMuSigNonceParam struct {
	SignerPosition int
	PublicNonce    []byte
}

// QiMuSigPartialParam is the partial-signature counterpart to QiMuSigNonceParam.
// We keep signer positions attached so both peers can merge bundle payloads
// without relying on sender/recipient-specific ordering conventions.
type QiMuSigPartialParam struct {
	SignerPosition   int
	PartialSignature []byte
}

type qiLocalBundleSession struct {
	position int
	session  *btcmusig2.Session
}

// QiMuSigBundleSession stores all local MuSig contexts needed to cooperatively
// sign one exact Qi transaction skeleton. This session is intentionally bound to
// one immutable TxIn/TxOut set so a peer cannot swap in a different transaction
// after nonce exchange has already started.
type QiMuSigBundleSession struct {
	mu sync.Mutex

	inner              *types.QiTx
	sigHash            [32]byte
	orderedSignerPubKs []*btcec.PublicKey
	localSessions      []qiLocalBundleSession
	localNonces        map[int][btcmusig2.PubNonceSize]byte
	registeredNonceHex map[int]string
	localPartials      map[int]*btcmusig2.PartialSignature
	finalSignature     *schnorr.Signature
	finalTxBytes       []byte
}

// NewQiMuSigBundleSession creates one btcec MuSig2 context per locally owned
// input key, but every context receives the full ordered signer set in final
// TxIn order. That TxIn-order invariant must match the node's validation logic
// exactly or the aggregate key will differ and the final signature will be
// rejected on chain.
func NewQiMuSigBundleSession(params *QiTxParams, localSigners map[int]*ecdsa.PrivateKey) (*QiMuSigBundleSession, [32]byte, []QiMuSigNonceParam, [][]byte, error) {
	if len(params.TxInputs) == 0 {
		return nil, [32]byte{}, nil, nil, fmt.Errorf("at least one Qi input is required")
	}
	if len(localSigners) == 0 {
		return nil, [32]byte{}, nil, nil, fmt.Errorf("at least one local signer is required")
	}

	inner, sigHash, err := buildUnsignedQiTx(params)
	if err != nil {
		return nil, [32]byte{}, nil, nil, err
	}

	orderedSignerPubKs := make([]*btcec.PublicKey, len(params.TxInputs))
	orderedSignerPubKeyBytes := make([][]byte, len(params.TxInputs))
	for index, txIn := range params.TxInputs {
		pubKey, err := btcec.ParsePubKey(txIn.PubKey)
		if err != nil {
			return nil, [32]byte{}, nil, nil, fmt.Errorf("failed to parse signer pubkey %d: %w", index, err)
		}
		orderedSignerPubKs[index] = pubKey
		orderedSignerPubKeyBytes[index] = append([]byte(nil), txIn.PubKey...)
	}

	localPositions := make([]int, 0, len(localSigners))
	for position := range localSigners {
		if position < 0 || position >= len(params.TxInputs) {
			return nil, [32]byte{}, nil, nil, fmt.Errorf("local signer position %d is outside tx input range", position)
		}
		localPositions = append(localPositions, position)
	}
	// Keep session creation deterministic so bundle outputs and debug logs are
	// stable across peers and test runs.
	for i := 0; i < len(localPositions)-1; i++ {
		for j := i + 1; j < len(localPositions); j++ {
			if localPositions[j] < localPositions[i] {
				localPositions[i], localPositions[j] = localPositions[j], localPositions[i]
			}
		}
	}

	session := &QiMuSigBundleSession{
		inner:              inner,
		sigHash:            sigHash,
		orderedSignerPubKs: orderedSignerPubKs,
		localSessions:      make([]qiLocalBundleSession, 0, len(localPositions)),
		localNonces:        make(map[int][btcmusig2.PubNonceSize]byte, len(localPositions)),
		registeredNonceHex: make(map[int]string, len(params.TxInputs)),
		localPartials:      make(map[int]*btcmusig2.PartialSignature, len(localPositions)),
	}

	nonceBundle := make([]QiMuSigNonceParam, 0, len(localPositions))
	for _, position := range localPositions {
		privKey := localSigners[position]
		btcPrivKey, _ := btcec.PrivKeyFromBytes(crypto.FromECDSA(privKey))
		ctx, err := btcmusig2.NewContext(
			btcPrivKey,
			false,
			btcmusig2.WithKnownSigners(orderedSignerPubKs),
		)
		if err != nil {
			return nil, [32]byte{}, nil, nil, fmt.Errorf("failed to create musig context for signer %d: %w", position, err)
		}
		bundleSession, err := ctx.NewSession()
		if err != nil {
			return nil, [32]byte{}, nil, nil, fmt.Errorf("failed to create musig session for signer %d: %w", position, err)
		}

		publicNonce := bundleSession.PublicNonce()
		session.localSessions = append(session.localSessions, qiLocalBundleSession{
			position: position,
			session:  bundleSession,
		})
		session.localNonces[position] = publicNonce
		nonceBundle = append(nonceBundle, QiMuSigNonceParam{
			SignerPosition: position,
			PublicNonce:    append([]byte(nil), publicNonce[:]...),
		})
	}

	return session, sigHash, nonceBundle, orderedSignerPubKeyBytes, nil
}

func (s *QiMuSigBundleSession) ensureRegisteredNonceSetLocked(remoteNonces []QiMuSigNonceParam) error {
	allNonces := make(map[int][btcmusig2.PubNonceSize]byte, len(s.orderedSignerPubKs))

	for position, localNonce := range s.localNonces {
		allNonces[position] = localNonce
	}
	for _, remoteNonce := range remoteNonces {
		if remoteNonce.SignerPosition < 0 || remoteNonce.SignerPosition >= len(s.orderedSignerPubKs) {
			return fmt.Errorf("remote nonce position %d is outside signer range", remoteNonce.SignerPosition)
		}
		if len(remoteNonce.PublicNonce) != btcmusig2.PubNonceSize {
			return fmt.Errorf("remote nonce %d has invalid size %d", remoteNonce.SignerPosition, len(remoteNonce.PublicNonce))
		}
		var nonce [btcmusig2.PubNonceSize]byte
		copy(nonce[:], remoteNonce.PublicNonce)
		allNonces[remoteNonce.SignerPosition] = nonce
	}

	if len(allNonces) != len(s.orderedSignerPubKs) {
		return fmt.Errorf("expected %d total nonces, got %d", len(s.orderedSignerPubKs), len(allNonces))
	}

	if len(s.registeredNonceHex) > 0 {
		for position, nonce := range allNonces {
			encoded := hex.EncodeToString(nonce[:])
			if existing := s.registeredNonceHex[position]; existing != encoded {
				return fmt.Errorf("nonce bundle changed for signer %d", position)
			}
		}
		return nil
	}

	// Each local btcec session must register all *other* signers' public nonces,
	// including same-device local signers. The wire payload is bundle-shaped, but
	// the local processing cost is still O(N²) because every local session needs
	// the full global nonce set minus itself.
	for _, local := range s.localSessions {
		for position, nonce := range allNonces {
			if position == local.position {
				continue
			}
			if _, err := local.session.RegisterPubNonce(nonce); err != nil {
				return fmt.Errorf("failed to register nonce for signer %d against local signer %d: %w", position, local.position, err)
			}
		}
	}

	for position, nonce := range allNonces {
		s.registeredNonceHex[position] = hex.EncodeToString(nonce[:])
	}
	return nil
}

func (s *QiMuSigBundleSession) encodePartialBundleLocked() ([]QiMuSigPartialParam, error) {
	partials := make([]QiMuSigPartialParam, 0, len(s.localSessions))
	for _, local := range s.localSessions {
		partial, ok := s.localPartials[local.position]
		if !ok {
			return nil, fmt.Errorf("missing partial signature for local signer %d", local.position)
		}
		var buffer bytes.Buffer
		if err := partial.Encode(&buffer); err != nil {
			return nil, fmt.Errorf("failed to encode partial signature for signer %d: %w", local.position, err)
		}
		partials = append(partials, QiMuSigPartialParam{
			SignerPosition:   local.position,
			PartialSignature: append([]byte(nil), buffer.Bytes()...),
		})
	}
	return partials, nil
}

// CreateLocalPartialBundle registers the remote nonce bundle (plus the already
// known local nonces) and returns one partial signature per locally owned input.
func (s *QiMuSigBundleSession) CreateLocalPartialBundle(remoteNonces []QiMuSigNonceParam) ([]QiMuSigPartialParam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.ensureRegisteredNonceSetLocked(remoteNonces); err != nil {
		return nil, err
	}
	if len(s.localPartials) == len(s.localSessions) {
		return s.encodePartialBundleLocked()
	}

	for _, local := range s.localSessions {
		if _, ok := s.localPartials[local.position]; ok {
			continue
		}
		partial, err := local.session.Sign(s.sigHash)
		if err != nil {
			return nil, fmt.Errorf("failed to create local partial for signer %d: %w", local.position, err)
		}
		s.localPartials[local.position] = partial
	}

	return s.encodePartialBundleLocked()
}

// FinalizeSignedTransaction combines the local and remote partial signatures,
// produces the final aggregate Schnorr signature, and returns the protobuf-encoded
// Qi transaction bytes. The first local session acts as the combiner because any
// fully prepared MuSig session can combine the remaining partials once all nonces
// have been registered.
func (s *QiMuSigBundleSession) FinalizeSignedTransaction(remoteNonces []QiMuSigNonceParam, remotePartials []QiMuSigPartialParam) ([]byte, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.finalTxBytes) > 0 && s.finalSignature != nil {
		return append([]byte(nil), s.finalTxBytes...), append([]byte(nil), s.finalSignature.Serialize()...), nil
	}
	if len(s.localSessions) == 0 {
		return nil, nil, fmt.Errorf("at least one local signer is required to finalize a bundle session")
	}

	if err := s.ensureRegisteredNonceSetLocked(remoteNonces); err != nil {
		return nil, nil, err
	}
	if len(s.localPartials) != len(s.localSessions) {
		for _, local := range s.localSessions {
			if _, ok := s.localPartials[local.position]; ok {
				continue
			}
			partial, err := local.session.Sign(s.sigHash)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to create local partial for signer %d: %w", local.position, err)
			}
			s.localPartials[local.position] = partial
		}
	}

	partialByPosition := make(map[int]*btcmusig2.PartialSignature, len(s.orderedSignerPubKs))
	for position, partial := range s.localPartials {
		partialByPosition[position] = partial
	}
	for _, remotePartial := range remotePartials {
		if remotePartial.SignerPosition < 0 || remotePartial.SignerPosition >= len(s.orderedSignerPubKs) {
			return nil, nil, fmt.Errorf("remote partial position %d is outside signer range", remotePartial.SignerPosition)
		}
		var partial btcmusig2.PartialSignature
		if err := partial.Decode(bytes.NewReader(remotePartial.PartialSignature)); err != nil {
			return nil, nil, fmt.Errorf("failed to decode remote partial %d: %w", remotePartial.SignerPosition, err)
		}
		partialByPosition[remotePartial.SignerPosition] = &partial
	}
	if len(partialByPosition) != len(s.orderedSignerPubKs) {
		return nil, nil, fmt.Errorf("expected %d partial signatures, got %d", len(s.orderedSignerPubKs), len(partialByPosition))
	}

	combiner := s.localSessions[0]
	for position := range s.orderedSignerPubKs {
		if position == combiner.position {
			continue
		}
		partial, ok := partialByPosition[position]
		if !ok {
			return nil, nil, fmt.Errorf("missing partial signature for signer %d", position)
		}
		if _, err := combiner.session.CombineSig(partial); err != nil {
			return nil, nil, fmt.Errorf("failed to combine partial for signer %d: %w", position, err)
		}
	}

	finalSig := combiner.session.FinalSig()
	if finalSig == nil {
		return nil, nil, fmt.Errorf("musig final signature not available")
	}

	s.inner.Signature = finalSig
	encodedTx, err := encodeSignedQiTx(s.inner)
	if err != nil {
		return nil, nil, err
	}
	s.finalSignature = finalSig
	s.finalTxBytes = append([]byte(nil), encodedTx...)

	return append([]byte(nil), encodedTx...), append([]byte(nil), finalSig.Serialize()...), nil
}

// VerifySignedQiTransaction mirrors the node's Qi signature check before the
// wallet sends a signed transaction to RPC. This prevents avoidable broadcast
// attempts when signer ordering, nonce registration, or partial combination went
// wrong on the client side.
func VerifySignedQiTransaction(protoTxBytes []byte, location common.Location) (bool, error) {
	tx, err := DecodeTransaction(protoTxBytes, location)
	if err != nil {
		return false, err
	}
	if tx.Type() != types.QiTxType {
		return false, fmt.Errorf("not a Qi transaction")
	}
	if len(tx.TxIn()) == 0 {
		return false, fmt.Errorf("Qi transaction must include at least one input")
	}

	pubKeys := make([]*btcec.PublicKey, len(tx.TxIn()))
	for index, txIn := range tx.TxIn() {
		pubKey, err := btcec.ParsePubKey(txIn.PubKey)
		if err != nil {
			return false, fmt.Errorf("failed to parse tx input pubkey %d: %w", index, err)
		}
		pubKeys[index] = pubKey
	}

	var finalKey *btcec.PublicKey
	if len(pubKeys) == 1 {
		finalKey = pubKeys[0]
	} else {
		aggKey, _, _, err := btcmusig2.AggregateKeys(pubKeys, false)
		if err != nil {
			return false, fmt.Errorf("failed to aggregate Qi input pubkeys: %w", err)
		}
		finalKey = aggKey.FinalKey
	}

	signer := types.NewSigner(tx.ChainId(), location)
	txDigest := signer.Hash(tx)
	return tx.GetSchnorrSignature().Verify(txDigest[:], finalKey), nil
}
