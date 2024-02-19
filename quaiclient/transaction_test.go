package quaiclient

import (
	"context"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quaiclient/ethclient"
)

var (
	location     = common.Location{0, 0}
	PARAMS       = params.ChainConfig{ChainID: big.NewInt(1337), Location: location}
	wsUrl        = "ws://localhost:8100"
	wsUrlCyprus2 = "ws://localhost:8101"
	MINERTIP     = big.NewInt(1 * params.GWei)
	BASEFEE      = big.NewInt(1 * params.GWei)
	GAS          = uint64(42000)
	VALUE        = big.NewInt(10)
)

func TestETX(t *testing.T) {
	fromAddress := common.HexToAddress("0x007c0C63038D8E099D6CDe00BBec41ca0d940D40", location)
	privKey, err := crypto.ToECDSA(common.FromHex("0xbc3f120802d74ee0136ab537bc423f9ae8b45a5da12298133790d939473c021c"))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey, location)
	if !from.Equal(fromAddress) {
		t.Fatalf("Failed to convert public key to address: %v", err)
	}

	toAddress := common.HexToAddress("0x107569E1b81B7c217062ed10e6d03e6e94a80DaC", common.Location{1, 0})
	toPrivKey, err := crypto.ToECDSA(common.FromHex("0x2541d7f6f17d4c65359bad46d82a48eacce266af8df72b982174f7ef9f934be2"))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	to := crypto.PubkeyToAddress(toPrivKey.PublicKey, common.Location{1, 0})
	if !to.Equal(toAddress) {
		t.Fatalf("Failed to convert public key to address: %v", err)
	}

	signer := types.LatestSigner(&PARAMS)

	wsClient, err := ethclient.Dial(wsUrl)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer wsClient.Close()

	nonce, err := wsClient.NonceAt(context.Background(), from, nil)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}

	inner_tx := types.InternalToExternalTx{ChainID: PARAMS.ChainID, Nonce: nonce, GasTipCap: MINERTIP, GasFeeCap: BASEFEE, ETXGasPrice: big.NewInt(9 * params.GWei), ETXGasLimit: 21000, ETXGasTip: big.NewInt(9 * params.GWei), Gas: GAS * 3, To: &to, Value: VALUE, Data: nil, AccessList: types.AccessList{}}
	tx := types.NewTx(&inner_tx)
	t.Log(tx.Hash().String())

	tx, err = types.SignTx(tx, signer, privKey)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}

	err = wsClient.SendTransaction(context.Background(), tx)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}

}

func TestGetBalance(t *testing.T) {
	wsClientCyprus1, err := ethclient.Dial(wsUrl)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer wsClientCyprus1.Close()

	balance, err := wsClientCyprus1.BalanceAt(context.Background(), common.HexToAddress("0x007c0C63038D8E099D6CDe00BBec41ca0d940D40", common.Location{0, 0}), nil)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	t.Log(balance)

	wsClientCyprus2, err := ethclient.Dial(wsUrlCyprus2)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer wsClientCyprus2.Close()

	balance, err = wsClientCyprus2.BalanceAt(context.Background(), common.HexToAddress("0x010978987B569072744dc9426E76590eb6fCfE8B", common.Location{0, 1}), nil)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	t.Log(balance)
}
