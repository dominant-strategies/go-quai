package hdwallet

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	quaiCommon "github.com/dominant-strategies/go-quai/common"
	qmath "github.com/dominant-strategies/go-quai/common/math"
	quaiCrypto "github.com/dominant-strategies/go-quai/crypto"
)

var typedDataReferenceTypeRegexp = regexp.MustCompile(`^[A-Za-z](\w*)(\[\d*\])*$`)

// TypedDataV4 models an EIP-712 typed data payload (eth_signTypedData_v4).
type TypedDataV4 struct {
	Types       TypedDataTypes   `json:"types"`
	PrimaryType string           `json:"primaryType"`
	Domain      TypedDataMessage `json:"domain"`
	Message     TypedDataMessage `json:"message"`
}

type TypedDataField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (t TypedDataField) isArray() bool {
	return strings.IndexByte(t.Type, '[') > 0
}

func (t TypedDataField) typeName() string {
	return strings.Split(t.Type, "[")[0]
}

type TypedDataTypes map[string][]TypedDataField
type TypedDataMessage map[string]interface{}

// ParseTypedDataV4JSON parses a JSON typed-data payload using json.Number preservation.
func ParseTypedDataV4JSON(data []byte) (*TypedDataV4, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	var td TypedDataV4
	if err := dec.Decode(&td); err != nil {
		return nil, err
	}
	if td.Domain == nil {
		td.Domain = TypedDataMessage{}
	}
	if td.Message == nil {
		td.Message = TypedDataMessage{}
	}
	return &td, nil
}

// TypedDataV4HashJSON computes the EIP-712 digest for a JSON payload.
func TypedDataV4HashJSON(typedDataJSON []byte) ([]byte, error) {
	td, err := ParseTypedDataV4JSON(typedDataJSON)
	if err != nil {
		return nil, err
	}
	return td.Hash()
}

// SignTypedDataV4 signs the EIP-712 digest for a JSON payload.
func SignTypedDataV4(privKey *ecdsa.PrivateKey, typedDataJSON []byte) ([]byte, error) {
	if privKey == nil {
		return nil, fmt.Errorf("private key is nil")
	}
	digest, err := TypedDataV4HashJSON(typedDataJSON)
	if err != nil {
		return nil, err
	}
	sig, err := quaiCrypto.Sign(digest, privKey)
	if err != nil {
		return nil, err
	}
	return normalizeSignatureV(sig), nil
}

// SignTypedDataV4 signs typed data with the private key for the given address.
func (w *HDWallet) SignTypedDataV4(address string, typedDataJSON []byte) ([]byte, error) {
	privKey, err := w.GetPrivateKeyForAddress(address)
	if err != nil {
		return nil, err
	}
	return SignTypedDataV4(privKey, typedDataJSON)
}

// VerifyTypedDataV4 recomputes the digest for a JSON payload (helper for tests/callers).
func VerifyTypedDataV4(typedDataJSON []byte) ([]byte, error) {
	return TypedDataV4HashJSON(typedDataJSON)
}

// Hash computes the EIP-712 digest: keccak256(0x1901 || domainSeparator || messageHash).
func (td *TypedDataV4) Hash() ([]byte, error) {
	if err := td.validate(); err != nil {
		return nil, err
	}
	domainSeparator, err := td.HashStruct("EIP712Domain", td.Domain)
	if err != nil {
		return nil, err
	}
	messageHash, err := td.HashStruct(td.PrimaryType, td.Message)
	if err != nil {
		return nil, err
	}
	prefix := []byte{0x19, 0x01}
	return quaiCrypto.Keccak256(prefix, domainSeparator, messageHash), nil
}

func (td *TypedDataV4) HashStruct(primaryType string, data TypedDataMessage) ([]byte, error) {
	encoded, err := td.EncodeData(primaryType, data, 1)
	if err != nil {
		return nil, err
	}
	return quaiCrypto.Keccak256(encoded), nil
}

func (td *TypedDataV4) Dependencies(primaryType string, found []string) []string {
	primaryType = strings.Split(primaryType, "[")[0]
	if slices.Contains(found, primaryType) {
		return found
	}
	if td.Types[primaryType] == nil {
		return found
	}
	found = append(found, primaryType)
	for _, field := range td.Types[primaryType] {
		for _, dep := range td.Dependencies(field.Type, found) {
			if !slices.Contains(found, dep) {
				found = append(found, dep)
			}
		}
	}
	return found
}

func (td *TypedDataV4) EncodeType(primaryType string) []byte {
	deps := td.Dependencies(primaryType, []string{})
	if len(deps) > 0 {
		sortedDeps := deps[1:]
		sort.Strings(sortedDeps)
		deps = append([]string{primaryType}, sortedDeps...)
	}

	var buf bytes.Buffer
	for _, dep := range deps {
		fields := td.Types[dep]
		if len(fields) == 0 {
			continue
		}
		buf.WriteString(dep)
		buf.WriteString("(")
		for _, f := range fields {
			buf.WriteString(f.Type)
			buf.WriteString(" ")
			buf.WriteString(f.Name)
			buf.WriteString(",")
		}
		buf.Truncate(buf.Len() - 1)
		buf.WriteString(")")
	}
	return buf.Bytes()
}

func (td *TypedDataV4) TypeHash(primaryType string) []byte {
	return quaiCrypto.Keccak256(td.EncodeType(primaryType))
}

func (td *TypedDataV4) EncodeData(primaryType string, data TypedDataMessage, depth int) ([]byte, error) {
	if err := td.validate(); err != nil {
		return nil, err
	}
	fields, ok := td.Types[primaryType]
	if !ok {
		return nil, fmt.Errorf("type %q not defined", primaryType)
	}

	var buf bytes.Buffer

	if data == nil {
		data = TypedDataMessage{}
	}
	if exp, got := len(fields), len(data); exp < got {
		return nil, fmt.Errorf("there is extra data provided in the message (%d < %d)", exp, got)
	}

	buf.Write(td.TypeHash(primaryType))
	for _, field := range fields {
		encType := field.Type
		encValue := data[field.Name]
		if len(encType) > 0 && encType[len(encType)-1] == ']' {
			encodedData, err := td.encodeArrayValue(encValue, encType, depth)
			if err != nil {
				return nil, err
			}
			buf.Write(encodedData)
			continue
		}
		if td.Types[field.Type] != nil {
			mapValue, ok := asTypedDataMessage(encValue)
			if !ok {
				return nil, dataMismatchError(encType, encValue)
			}
			encodedData, err := td.EncodeData(field.Type, mapValue, depth+1)
			if err != nil {
				return nil, err
			}
			buf.Write(quaiCrypto.Keccak256(encodedData))
			continue
		}

		byteValue, err := td.EncodePrimitiveValue(encType, encValue, depth)
		if err != nil {
			return nil, err
		}
		buf.Write(byteValue)
	}

	return buf.Bytes(), nil
}

func asTypedDataMessage(v interface{}) (TypedDataMessage, bool) {
	switch m := v.(type) {
	case nil:
		return nil, false
	case TypedDataMessage:
		return m, true
	case map[string]interface{}:
		return TypedDataMessage(m), true
	default:
		return nil, false
	}
}

func (td *TypedDataV4) encodeArrayValue(encValue interface{}, encType string, depth int) ([]byte, error) {
	arrayValue, err := convertDataToSlice(encValue)
	if err != nil {
		return nil, dataMismatchError(encType, encValue)
	}

	arrayBuf := new(bytes.Buffer)
	parsedType := strings.Split(encType, "[")[0]
	for _, item := range arrayValue {
		if item == nil {
			return nil, dataMismatchError(encType, encValue)
		}

		rt := reflect.TypeOf(item)
		if rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
			var encoded []byte
			if rt.Elem().Kind() == reflect.Uint8 {
				encoded, err = td.EncodePrimitiveValue(parsedType, item, depth+1)
			} else {
				encoded, err = td.encodeArrayValue(item, parsedType, depth+1)
			}
			if err != nil {
				return nil, err
			}
			arrayBuf.Write(encoded)
			continue
		}

		if td.Types[parsedType] != nil {
			mapValue, ok := asTypedDataMessage(item)
			if !ok {
				return nil, dataMismatchError(parsedType, item)
			}
			encodedData, err := td.EncodeData(parsedType, mapValue, depth+1)
			if err != nil {
				return nil, err
			}
			arrayBuf.Write(quaiCrypto.Keccak256(encodedData))
			continue
		}

		bytesValue, err := td.EncodePrimitiveValue(parsedType, item, depth)
		if err != nil {
			return nil, err
		}
		arrayBuf.Write(bytesValue)
	}

	return quaiCrypto.Keccak256(arrayBuf.Bytes()), nil
}

func parseBytesValue(encValue interface{}) ([]byte, bool) {
	rv := reflect.ValueOf(encValue)
	if rv.IsValid() && rv.Kind() == reflect.Array && rv.Type().Elem().Kind() == reflect.Uint8 {
		out := make([]byte, rv.Len())
		reflect.Copy(reflect.ValueOf(out), rv)
		return out, true
	}

	switch v := encValue.(type) {
	case []byte:
		return v, true
	case string:
		if strings.HasPrefix(v, "0x") || strings.HasPrefix(v, "0X") {
			return quaiCommon.FromHex(v), true
		}
		return []byte(v), true
	default:
		return nil, false
	}
}

func parseIntegerValue(encType string, encValue interface{}) (*big.Int, error) {
	var (
		length int
		signed = strings.HasPrefix(encType, "int")
		b      *big.Int
	)

	if encType == "int" || encType == "uint" {
		length = 256
	} else {
		var lengthStr string
		if strings.HasPrefix(encType, "uint") {
			lengthStr = strings.TrimPrefix(encType, "uint")
		} else {
			lengthStr = strings.TrimPrefix(encType, "int")
		}
		size, err := strconv.Atoi(lengthStr)
		if err != nil {
			return nil, fmt.Errorf("invalid size on integer: %v", lengthStr)
		}
		length = size
	}

	switch v := encValue.(type) {
	case *big.Int:
		b = new(big.Int).Set(v)
	case big.Int:
		b = new(big.Int).Set(&v)
	case json.Number:
		if bi, ok := qmath.ParseBig256(v.String()); ok {
			b = bi
		} else {
			return nil, fmt.Errorf("invalid integer value %v for type %v", v, encType)
		}
	case string:
		if bi, ok := qmath.ParseBig256(v); ok {
			b = bi
		} else {
			return nil, fmt.Errorf("invalid integer value %v for type %v", encValue, encType)
		}
	case float64:
		if float64(int64(v)) == v {
			b = big.NewInt(int64(v))
		} else {
			return nil, fmt.Errorf("invalid float value %v for type %v", v, encType)
		}
	case int:
		b = big.NewInt(int64(v))
	case int64:
		b = big.NewInt(v)
	case uint64:
		b = new(big.Int).SetUint64(v)
	case nil:
		return nil, fmt.Errorf("invalid integer value <nil> for type %v", encType)
	}

	if b == nil {
		return nil, fmt.Errorf("invalid integer value %v/%v for type %v", encValue, reflect.TypeOf(encValue), encType)
	}
	if b.BitLen() > length {
		return nil, fmt.Errorf("integer larger than '%v'", encType)
	}
	if !signed && b.Sign() < 0 {
		return nil, fmt.Errorf("invalid negative value for unsigned type %v", encType)
	}
	return b, nil
}

func (td *TypedDataV4) EncodePrimitiveValue(encType string, encValue interface{}, depth int) ([]byte, error) {
	_ = depth
	switch encType {
	case "address":
		ret := make([]byte, 32)
		switch v := encValue.(type) {
		case string:
			if quaiCommon.IsHexAddress(v) {
				addr := quaiCommon.HexToAddressBytes(v)
				copy(ret[12:], addr.Bytes())
				return ret, nil
			}
		case []byte:
			if len(v) == 20 {
				copy(ret[12:], v)
				return ret, nil
			}
		case [20]byte:
			copy(ret[12:], v[:])
			return ret, nil
		}
		return nil, dataMismatchError(encType, encValue)
	case "bool":
		boolValue, ok := encValue.(bool)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		if boolValue {
			return qmath.PaddedBigBytes(quaiCommon.Big1, 32), nil
		}
		return qmath.PaddedBigBytes(quaiCommon.Big0, 32), nil
	case "string":
		s, ok := encValue.(string)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		return quaiCrypto.Keccak256([]byte(s)), nil
	case "bytes":
		b, ok := parseBytesValue(encValue)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		return quaiCrypto.Keccak256(b), nil
	}

	if strings.HasPrefix(encType, "bytes") {
		lengthStr := strings.TrimPrefix(encType, "bytes")
		length, err := strconv.Atoi(lengthStr)
		if err != nil {
			return nil, fmt.Errorf("invalid size on bytes: %v", lengthStr)
		}
		if length < 0 || length > 32 {
			return nil, fmt.Errorf("invalid size on bytes: %d", length)
		}
		byteValue, ok := parseBytesValue(encValue)
		if !ok || len(byteValue) != length {
			return nil, dataMismatchError(encType, encValue)
		}
		dst := make([]byte, 32)
		copy(dst, byteValue)
		return dst, nil
	}

	if strings.HasPrefix(encType, "int") || strings.HasPrefix(encType, "uint") {
		b, err := parseIntegerValue(encType, encValue)
		if err != nil {
			return nil, err
		}
		return qmath.U256Bytes(new(big.Int).Set(b)), nil
	}

	return nil, fmt.Errorf("unrecognized type '%s'", encType)
}

func dataMismatchError(encType string, encValue interface{}) error {
	return fmt.Errorf("provided data '%v' doesn't match type '%s'", encValue, encType)
}

func convertDataToSlice(encValue interface{}) ([]interface{}, error) {
	var out []interface{}
	rv := reflect.ValueOf(encValue)
	if !rv.IsValid() {
		return out, fmt.Errorf("provided data '%v' is not slice", encValue)
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return out, fmt.Errorf("provided data '%v' is not slice", encValue)
	}
	for i := 0; i < rv.Len(); i++ {
		out = append(out, rv.Index(i).Interface())
	}
	return out, nil
}

func (td *TypedDataV4) validate() error {
	if td == nil {
		return fmt.Errorf("typed data is nil")
	}
	if err := td.Types.validate(); err != nil {
		return err
	}
	if td.PrimaryType == "" {
		return fmt.Errorf("primaryType is undefined")
	}
	if td.Types[td.PrimaryType] == nil {
		return fmt.Errorf("primary type %q is undefined", td.PrimaryType)
	}
	if td.Domain == nil {
		return fmt.Errorf("domain is undefined")
	}
	// EIP712Domain can be empty in the message but the type definition should exist for v4.
	if _, ok := td.Types["EIP712Domain"]; !ok {
		return fmt.Errorf("EIP712Domain type is undefined")
	}
	return nil
}

func (t TypedDataTypes) validate() error {
	for typeKey, typeArr := range t {
		if len(typeKey) == 0 {
			return fmt.Errorf("empty type key")
		}
		for i, typeObj := range typeArr {
			if len(typeObj.Type) == 0 {
				return fmt.Errorf("type %q:%d: empty Type", typeKey, i)
			}
			if len(typeObj.Name) == 0 {
				return fmt.Errorf("type %q:%d: empty Name", typeKey, i)
			}
			if typeKey == typeObj.Type {
				return fmt.Errorf("type %q cannot reference itself", typeObj.Type)
			}
			if isPrimitiveTypeValid(typeObj.Type) {
				continue
			}
			if _, exist := t[typeObj.typeName()]; !exist {
				return fmt.Errorf("reference type %q is undefined", typeObj.Type)
			}
			if !typedDataReferenceTypeRegexp.MatchString(typeObj.Type) {
				return fmt.Errorf("unknown reference type %q", typeObj.Type)
			}
		}
	}
	return nil
}

var validPrimitiveTypes = map[string]struct{}{}

func init() {
	for _, t := range []string{
		"address", "address[]", "bool", "bool[]", "string", "string[]",
		"bytes", "bytes[]", "int", "int[]", "uint", "uint[]",
	} {
		validPrimitiveTypes[t] = struct{}{}
	}
	for n := 1; n <= 32; n++ {
		validPrimitiveTypes[fmt.Sprintf("bytes%d", n)] = struct{}{}
		validPrimitiveTypes[fmt.Sprintf("bytes%d[]", n)] = struct{}{}
	}
	for n := 8; n <= 256; n += 8 {
		validPrimitiveTypes[fmt.Sprintf("int%d", n)] = struct{}{}
		validPrimitiveTypes[fmt.Sprintf("int%d[]", n)] = struct{}{}
		validPrimitiveTypes[fmt.Sprintf("uint%d", n)] = struct{}{}
		validPrimitiveTypes[fmt.Sprintf("uint%d[]", n)] = struct{}{}
	}
}

func isPrimitiveTypeValid(primitiveType string) bool {
	input := strings.Split(primitiveType, "[")[0]
	_, ok := validPrimitiveTypes[input]
	return ok
}
