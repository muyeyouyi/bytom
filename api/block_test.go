package api

import (
	"io/ioutil"
	"os"
	"testing"
	"time"
	"errors"
	"fmt"

	dbm "github.com/tendermint/tmlibs/db"

	"github.com/bytom/account"
	"github.com/bytom/blockchain/txbuilder"
	"github.com/bytom/config"
	"github.com/bytom/consensus"
	"github.com/bytom/consensus/difficulty"
	"github.com/bytom/database/leveldb"
	"github.com/bytom/database/storage"
	"github.com/bytom/mining"
	"github.com/bytom/protocol"
	"github.com/bytom/protocol/state"
	"github.com/bytom/protocol/bc"
	"github.com/bytom/protocol/bc/types"
)

func TestNewBlock(t *testing.T) {
	dirPath, err := ioutil.TempDir(".", "testDB")
	if err != nil {
		t.Fatal(err)
	}

	testDB := dbm.NewDB("testdb", "leveldb", dirPath)
	store := leveldb.NewStore(testDB)
	defer os.RemoveAll(dirPath)

	// generate utxos and transactions
	utxos := mockUtxos(10)
	txs, err := GenetrateTx(utxos, len(utxos))
	if err != nil {
		t.Fatal("generate tx err:", err)
	}

	// init UtxoViewpoint
	utxoView := state.NewUtxoViewpoint()
	utxoEntry := storage.NewUtxoEntry(false, 0, false)
	for _, tx := range txs {
		for _, id := range tx.SpentOutputIDs {
			utxoView.Entries[id] = utxoEntry
			fmt.Println("-----------spendoutputid:", id.String())
		}
	}

	//testBlock := &types.Block{}
	//testBlock.Version = 1
	//
	//if err := store.SaveChainStatus(testBlock, utxoView,nil); err != nil {
	//	t.Fatal("save utxoView err:", err)
	//}

	if err := store.SaveUtxoView(utxoView); err != nil {
		t.Fatal("SaveUtxoView err:", err)
	}

	genesisBlock := config.GenerateGenesisBlock()
	txPool := protocol.NewTxPool()
	chain, err := protocol.NewChain(genesisBlock.Hash(), store, txPool)
	if err != nil {
		t.Fatal("Failed to create chain structure:", err)
	}

	if chain.BestBlockHash() == nil {
		if err := chain.SaveBlock(genesisBlock); err != nil {
			t.Fatal("Failed to save genesisBlock to store:", err)
		}
		if err := chain.ConnectBlock(genesisBlock); err != nil {
			t.Fatal("Failed to connect genesisBlock to chain:", err)
		}
	}

	for i := 0; i < 10; i++ {
		if err := InsertChain(chain, txs); err != nil {
			t.Fatal("Failed to insert block into chain:", err)
		}
	}
}

func InsertChain(chain *protocol.Chain, txs []*types.Tx) error {
	if err := InsertTxPool(chain, txs); err != nil {
		return err
	}

	block, err := NewBlock(chain)
	if err != nil {
		return err
	}

	seed, err := chain.GetSeed(block.Height, &block.PreviousBlockHash)
	if err != nil {
		return err
	}

	if err := SolveBlock(seed, block); err != nil {
		return err
	}

	if err := chain.SaveBlock(block); err != nil {
		return err
	}

	if err := chain.ConnectBlock(block); err != nil {
		return err
	}

	return nil
}

func NewBlock(chain *protocol.Chain) (b *types.Block, err error) {
	txpool := chain.GetTxPool()
	return mining.NewBlockTemplate(chain, txpool, nil)
}

func SolveBlock(seed *bc.Hash, block *types.Block) error {
	maxNonce := ^uint64(0) // 2^64 - 1
	header := &block.BlockHeader
	for i := uint64(0); i < maxNonce; i++ {
		header.Nonce = i
		headerHash := header.Hash()
		if difficulty.CheckProofOfWork(&headerHash, seed, header.Bits) {
			return nil
		}
	}
	return nil
}

func mockUtxos(num int) []*account.UTXO {
	utxos := []*account.UTXO{}
	for i:= 0; i< num;i++{
		utxo := MockUtxo(uint64(i),624000000000)
		utxos = append(utxos, utxo)
	}

	return utxos
}

func InsertTxPool(chain *protocol.Chain, txs []*types.Tx) error {
	for _, tx := range txs {
		if err := txbuilder.FinalizeTx(nil, chain, tx); err != nil {
			return err
		}
	}

	return nil
}

func MockUtxo(index, amount uint64) *account.UTXO {
	ctrlProg := &account.CtrlProgram{
		AccountID:      "",
		Address:        "",
		KeyIndex:       uint64(0),
		ControlProgram: []byte{81},
		Change:         false,
	}

	assetAmount := bc.AssetAmount{
		AssetId: consensus.BTMAssetID,
		Amount:  amount,
	}

	utxo := &account.UTXO{
		OutputID:            bc.Hash{V0: 1},
		SourceID:            bc.Hash{V0: 1},
		AssetID:             *assetAmount.AssetId,
		Amount:              assetAmount.Amount,
		SourcePos:           index,
		ControlProgram:      ctrlProg.ControlProgram,
		ControlProgramIndex: ctrlProg.KeyIndex,
		AccountID:           ctrlProg.AccountID,
		Address:             ctrlProg.Address,
		ValidHeight:         0,
	}

	return utxo
}

func AddTxInputFromUtxo(utxo *account.UTXO) (*types.TxInput, *txbuilder.SigningInstruction, error) {
	txInput, signInst, err := account.UtxoToInputs(nil, utxo)
	if err != nil {
		return nil, nil, err
	}

	return txInput, signInst, nil
}

func AddTxOutput(assetID bc.AssetID, amount uint64, controlProgram []byte) *types.TxOutput {
	out := types.NewTxOutput(assetID, amount, controlProgram)
	return out
}

func CreateTxBuilder(utxo *account.UTXO) (*txbuilder.TemplateBuilder, error) {
	tplBuilder := txbuilder.NewBuilder(time.Now())

	// add input
	txInput, signInst, err := AddTxInputFromUtxo(utxo)
	if err != nil {
		return nil, err
	}
	tplBuilder.AddInput(txInput, signInst)

	txOutput := AddTxOutput(utxo.AssetID, utxo.Amount-uint64(10000000), utxo.ControlProgram)
	tplBuilder.AddOutput(txOutput)

	return tplBuilder, nil

}

func BuildTx(utxo *account.UTXO) (*types.Tx, error) {
	tplBuilder, err := CreateTxBuilder(utxo)
	if err != nil {
		return nil, err
	}

	tpl, _, err := tplBuilder.Build()
	if err != nil {
		return nil, err
	}

	return tpl.Transaction, nil
}

func GenetrateTx(utxo []*account.UTXO, num int) ([]*types.Tx, error) {
	if num < len(utxo) {
		return nil, errors.New("utxo is not enough")
	}

	txs := []*types.Tx{}
	for i:= 0; i<num; i++{
		tx, err := BuildTx(utxo[i])
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
	}

	return txs, nil
}
