package quaiclient

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/dominant-strategies/go-quai/common"
	"github.com/dominant-strategies/go-quai/common/hexutil"
	"github.com/dominant-strategies/go-quai/core/types"
	"github.com/dominant-strategies/go-quai/crypto"
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

func TestQiConversion(t *testing.T) {
	client, err := ethclient.Dial(wsUrl)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer client.Close()
	//fromAddr := common.HexToAddress("0x00899DD5871a40E2c67d0645B8DcEb4Dc7974a59")
	privkey := common.FromHex(qiPrivkey) //"383bd2269958a23e0391be01d255316363e2fa22269cbdc48052343346a4dcd8")
	schnorrPrivKey, schnorrPubKey := btcec.PrivKeyFromBytes(privkey)
	//0x625c4e3d17bbf9ad748d822b0359c36f52786198b58acd02fe05c207a600a0cdETXIndex:
	toAddress := common.HexToAddress(quaiAddr, location)
	outpointHash := common.HexToHash("0x00ed00d45972a31b8be11a0bde8007aa65bc0fbb115374fa7e554c44ec45a46e")
	outpointIndex := uint16(0)
	prevOut := types.OutPoint{outpointHash, outpointIndex}

	in := types.TxIn{
		PreviousOutPoint: prevOut,
		PubKey:           schnorrPubKey.SerializeUncompressed(),
	}

	newOut := types.TxOut{
		Denomination: 15,
		Address:      toAddress.Bytes(),
	}

	qiTx := &types.QiTx{
		ChainID: big.NewInt(1337),
		TxIn:    []types.TxIn{in},
		TxOut:   []types.TxOut{newOut},
	}
	signer := types.LatestSigner(&PARAMS)
	txDigestHash := signer.Hash(types.NewTx(qiTx))
	sig, err := schnorr.Sign(schnorrPrivKey, txDigestHash[:])
	if err != nil {
		t.Errorf("Failed to sign transaction: %v", err)
		return
	}
	qiTx.Signature = sig
	fmt.Println("Signature: " + hexutil.Encode(qiTx.Signature.Serialize()))
	if !sig.Verify(txDigestHash[:], schnorrPubKey) {
		t.Error("Failed to verify signature")
		return
	}

	fmt.Println("Signed Raw Transaction")
	fmt.Println("Signature:", common.Bytes2Hex(sig.Serialize()))
	fmt.Println("TX Digest Hash", txDigestHash.String())
	fmt.Println("Pubkey", common.Bytes2Hex(qiTx.TxIn[0].PubKey))

	// Send the transaction
	err = client.SendTransaction(context.Background(), types.NewTx(qiTx))
	if err != nil {
		fmt.Println(err.Error())
		return
	}

}

func TestQuaiConversion(t *testing.T) {
	fromAddress := common.HexToAddress(quaiAddr, location)
	privKey, err := crypto.ToECDSA(common.FromHex(quaiPrivkey))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey, location)
	if !from.Equal(fromAddress) {
		t.Fatalf("Failed to convert public key to address: %v", err)
	}
	toAddress := common.HexToAddress("0x00e8c50233D309e5e63805D6d7AE10e6EDE83c65", location)
	toPrivKey, err := crypto.ToECDSA(common.FromHex("0x2f156531b49753994351ae3cb446264993dcdb21276558ff9f4126d6129ea21c"))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	to := crypto.PubkeyToAddress(toPrivKey.PublicKey, location)
	if !to.Equal(toAddress) {
		t.Fatalf("Failed to convert public key to address: %v", err)
	}
	signer := types.LatestSigner(&PARAMS)

	client, err := ethclient.Dial(wsUrl)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer client.Close()

	nonce, err := client.PendingNonceAt(context.Background(), from)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	// Check balance
	balance, err := client.BalanceAt(context.Background(), fromAddress, nil)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Println("Balance: ", balance)
	fmt.Println("Nonce: ", nonce)

	inner_tx := types.QuaiTx{ChainID: PARAMS.ChainID, Nonce: nonce, GasTipCap: MINERTIP, GasFeeCap: BASEFEE, Gas: GAS * 10, To: &to /*Value: params.MinQuaiConversionAmount,*/, Data: nil, AccessList: types.AccessList{}}
	tx := types.NewTx(&inner_tx)
	tx, err = types.SignTx(tx, signer, privKey)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}

	err = client.SendTransaction(context.Background(), tx)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}
	time.Sleep(60 * time.Second)
	tx, isPending, err := client.TransactionByHash(context.Background(), tx.Hash(location...))
	fmt.Printf("tx: %+v isPending: %v err: %v\n", tx, isPending, err)
	receipt, err := client.TransactionReceipt(context.Background(), tx.Hash())
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Printf("Receipt: %+v\n", receipt)

	etx := receipt.Etxs[0]
	tx, isPending, err = client.TransactionByHash(context.Background(), etx.Hash())
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Printf("etx: %+v isPending: %v err: %v\n", tx, isPending, err)
	receipt, err = client.TransactionReceipt(context.Background(), etx.Hash())
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Printf("ETX Receipt: %+v\n", receipt)
}

func TestRedeemQuai(t *testing.T) {
	client, err := ethclient.Dial(wsUrl)
	if err != nil {
		t.Fatalf("Failed to connect to the Ethereum WebSocket client: %v", err)
	}
	defer client.Close()
	fromAddress := common.HexToAddress(quaiAddr, location)
	privKey, err := crypto.ToECDSA(common.FromHex(quaiPrivkey))
	if err != nil {
		t.Fatalf("Failed to convert private key to ECDSA: %v", err)
	}
	from := crypto.PubkeyToAddress(privKey.PublicKey, location)
	if !from.Equal(fromAddress) {
		t.Fatalf("Failed to convert public key to address: %v", err)
	}
	toAddr := common.HexToAddress(fmt.Sprintf("0x%x0000000000000000000000000000000000000A", location.BytePrefix()), location)
	fmt.Println("To Address: ", toAddr.String())
	signer := types.LatestSigner(&PARAMS)
	nonce, err := client.PendingNonceAt(context.Background(), from)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	// Check balance
	balance, err := client.BalanceAt(context.Background(), fromAddress, nil)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Println("Balance: ", balance)
	fmt.Println("Nonce: ", nonce)
	inner_tx := types.QuaiTx{ChainID: PARAMS.ChainID, Nonce: nonce, GasTipCap: MINERTIP, GasFeeCap: BASEFEE, Gas: GAS * 10, To: &toAddr, Value: common.Big0, Data: nil, AccessList: types.AccessList{}}

	tx := types.NewTx(&inner_tx)
	tx, err = types.SignTx(tx, signer, privKey)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}

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
	nonce, err = client.PendingNonceAt(context.Background(), from)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}
	// Check balance
	balance, err = client.BalanceAt(context.Background(), fromAddress, nil)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	fmt.Println("Balance: ", balance)
	fmt.Println("Nonce: ", nonce)
}
