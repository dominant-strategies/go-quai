package quaiclient

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/core/types"
	goCrypto "github.com/dominant-strategies/go-quai/crypto"

	"github.com/dominant-strategies/go-quai/params"
	"github.com/dominant-strategies/go-quai/quaiclient/ethclient"
)

var (
	location = common.Location{0, 0}
	PARAMS   = params.ChainConfig{ChainID: big.NewInt(1337), Location: location}
	MINERTIP = big.NewInt(1 * params.GWei)
	BASEFEE  = big.NewInt(1 * params.GWei)
	GAS      = uint64(420000)
	VALUE    = big.NewInt(10)
)

func TestTX(t *testing.T) {

	numTests := 1
	fromAddress := make([]common.Address, numTests)
	privKey := make([]*ecdsa.PrivateKey, numTests)
	toAddress := make([]common.Address, numTests)
	// toPrivKey := make([]*ecdsa.PrivateKey, numTests)
	wsUrl := make([]string, numTests)
	err := error(nil)
	fromLocation := make([]common.Location, numTests)
	toLocation := make([]common.Location, numTests)

	//cyprus 1 -> cyprus 1
	fromLocation[0] = common.Location{0, 0}
	toLocation[0] = common.Location{0, 0}
	fromAddress[0] = common.HexToAddress("0x0021358CeaC22936858C3eDa6EB86e0559915550", fromLocation[0])
	privKey[0], err = goCrypto.ToECDSA(common.FromHex("0x7e99ffbdf4b3dda10174f18a0991114bb4a7a684b5972c6901fbe8a4a4bfc325"))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	toAddress[0] = common.HexToAddress("0x0147f9CEa7662C567188D58640ffC48901cde02a", toLocation[0])
	// toPrivKey[0], err = goCrypto.ToECDSA(common.FromHex("0x86f3731e698525a27530d4da6d1ae826303bb9b813ee718762b4c3524abddac5"))
	// if err != nil {
	// 	t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	// }
	wsUrl[0] = "ws://localhost:8100"
	to := toAddress[0]

	for i := 0; i < numTests; i++ {
		from := goCrypto.PubkeyToAddress(privKey[i].PublicKey, fromLocation[i])
		if !from.Equal(fromAddress[i]) {
			t.Fatalf("Failed to convert public key to address: %v", err)
		}

		// to := goCrypto.PubkeyToAddress(toPrivKey[i].PublicKey, toLocation[i])
		// if !to.Equal(toAddress[i]) {
		// 	t.Fatalf("Failed to convert public key to address: %v", err)
		// }

		signer := types.LatestSigner(&PARAMS)

		wsClient, err := ethclient.Dial(wsUrl[i])
		if err != nil {
			t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
		}
		defer wsClient.Close()

		nonce, err := wsClient.NonceAt(context.Background(), from, nil)

		if err != nil {
			t.Error(err.Error())
			t.Fail()
		}

		inner_tx := types.QuaiTx{ChainID: PARAMS.ChainID, Nonce: nonce, GasTipCap: MINERTIP, GasFeeCap: BASEFEE, Gas: GAS * 3, To: &to, Value: VALUE, Data: nil, AccessList: types.AccessList{}}
		tx := types.NewTx(&inner_tx)

		tx, err = types.SignTx(tx, signer, privKey[i])
		if err != nil {
			t.Error(err.Error())
			t.Fail()
		}

		t.Log(tx.Hash().String())

		err = wsClient.SendTransaction(context.Background(), tx)
		if err != nil {
			t.Error(err.Error())
			t.Fail()
		}

	}
}

func TestGetBalance(t *testing.T) {
	wsUrl := "ws://localhost:8100"
	wsUrlCyprus2 := "ws://localhost:8101"
	wsClientCyprus1, err := ethclient.Dial(wsUrl)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer wsClientCyprus1.Close()

	balance, err := wsClientCyprus1.BalanceAt(context.Background(), common.HexToAddress("0x0047f9CEa7662C567188D58640ffC48901cde02a", common.Location{0, 0}), nil)
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

	balance, err = wsClientCyprus2.BalanceAt(context.Background(), common.HexToAddress("0x01736f9273a0dF59619Ea4e17c284b422561819e", common.Location{0, 1}), nil)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	t.Log(balance)
}
