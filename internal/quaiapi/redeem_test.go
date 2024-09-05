package quaiapi

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quaiclient/ethclient"
	"google.golang.org/protobuf/proto"
)

var (
	MAXFEE   = big.NewInt(1 * params.GWei)
	BASEFEE  = MAXFEE
	MINERTIP = big.NewInt(1 * params.GWei)
	GAS      = uint64(21000)
	VALUE    = big.NewInt(1)
	location = common.Location{}
	// Change the params to the proper chain config
	PARAMS = params.Blake3PowLocalChainConfig
	// wsUrlCyprus2 = "ws://127.0.0.1:8201"
	wsUrl_ = "ws://127.0.0.1:8200"
	// wsUrl_       = "ws://34.27.106.110:8200"

	quaiGenAllocAddr    = "0x002a8cf994379232561556Da89C148eeec9539cd"
	quaiGenAllocPrivKey = "0xefdc32bef4218d3e5bae3858e45d4f18ed257c617bd8b7bae0939fae6f6bd6d6"
)

func TestRedeemQuai(t *testing.T) {
	client, err := ethclient.Dial(wsUrl_)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer client.Close()
	fromAddress := common.HexToAddress(quaiGenAllocAddr, location)
	privKey, err := crypto.ToECDSA(common.FromHex(quaiGenAllocPrivKey))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey, location)
	if !from.Equal(fromAddress) {
		t.Fatalf("Failed to convert public key to address: %v", err)
	}
	toAddr := common.HexToAddress("", location)
	fmt.Println("To Address: ", toAddr.String())
	signer := types.LatestSigner(PARAMS)
	nonce, err := client.PendingNonceAt(context.Background(), from.MixedcaseAddress())
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	// Check balance
	balance, err := client.BalanceAt(context.Background(), fromAddress.MixedcaseAddress(), nil)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Println("Balance: ", balance)
	fmt.Println("Nonce: ", nonce)
	inner_tx := types.QuaiTx{ChainID: PARAMS.ChainID, Nonce: nonce, GasTipCap: MINERTIP, GasFeeCap: BASEFEE, Gas: GAS, To: &toAddr, Value: common.Big1, Data: nil, AccessList: types.AccessList{}}

	tx := types.NewTx(&inner_tx)
	tx, err = types.SignTx(tx, signer, privKey)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}
	protoTx, err := tx.ProtoEncode()
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}
	protoTxBytes, err := proto.Marshal(protoTx)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}
	fmt.Println(hex.EncodeToString(protoTxBytes))
	err = client.SendTransaction(context.Background(), tx)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}
	tx, isPending, err := client.TransactionByHash(context.Background(), tx.Hash(location...))
	fmt.Printf("tx: %+v isPending: %v err: %v\n", tx, isPending, err)
	receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Printf("Receipt: %+v\n", receipt)
	nonce, err = client.PendingNonceAt(context.Background(), from.MixedcaseAddress())
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	// Check balance
	balance, err = client.BalanceAt(context.Background(), fromAddress.MixedcaseAddress(), nil)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Println("Balance: ", balance)
	fmt.Println("Nonce: ", nonce)
}
