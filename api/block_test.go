package api

import (
	"io/ioutil"
	"os"
	"testing"
	"time"
	"errors"

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
	testNumber := 10
	blockTxNumber := 5
	totalTxNumber := testNumber * blockTxNumber

	chain, txs, err := GenerateChainData(totalTxNumber)
	if err != nil {
		t.Fatal("GenerateChainData err:", err)
	}

	for i := 0; i < testNumber; i++ {
		testTxs := txs[blockTxNumber*i : blockTxNumber*(i+1)]
		if err := InsertChain(chain, testTxs); err != nil {
			t.Fatal("Failed to insert block into chain:", err)
		}
	}
}

func GenerateChainData(txNumber int)  (*protocol.Chain, []*types.Tx, error) {
	dirPath, err := ioutil.TempDir(".", "testDB")
	if err != nil {
		return nil, nil, err
	}

	testDB := dbm.NewDB("testdb", "leveldb", dirPath)
	defer os.RemoveAll(dirPath)

	// generate utxos and transactions
	baseUtxos := GenerateBaseUtxos(txNumber)
	otherUtxos := GenerateOtherUtxos(txNumber)
	txs, err := GenetrateTx(baseUtxos, otherUtxos, len(baseUtxos))
	if err != nil {
		return nil, nil, err
	}

	// init UtxoViewpoint
	utxoView := state.NewUtxoViewpoint()
	utxoEntry := storage.NewUtxoEntry(false, 1, false)
	for _, tx := range txs {
		for _, id := range tx.SpentOutputIDs {
			utxoView.Entries[id] = utxoEntry
		}
	}

	if err := SetUtxoView(testDB, utxoView); err != nil {
		return nil, nil, err
	}

	genesisBlock := config.GenerateGenesisBlock()
	txPool := protocol.NewTxPool()
	store := leveldb.NewStore(testDB)
	chain, err := protocol.NewChain(genesisBlock.Hash(), store, txPool)
	if err != nil {
		return nil, nil, err
	}

	if chain.BestBlockHash() == nil {
		if err := chain.SaveBlock(genesisBlock); err != nil {
			return nil, nil, err
		}
		if err := chain.ConnectBlock(genesisBlock); err != nil {
			return nil, nil, err
		}
	}

	return chain, txs, nil
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

func InsertTxPool(chain *protocol.Chain, txs []*types.Tx) error {
	for _, tx := range txs {
		if err := txbuilder.FinalizeTx(nil, chain, tx); err != nil {
			return err
		}
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

func MockUtxo(index uint64, assetId *bc.AssetID, amount uint64) *account.UTXO {
	ctrlProg := &account.CtrlProgram{
		AccountID:      "",
		Address:        "",
		KeyIndex:       uint64(0),
		ControlProgram: []byte{81},
		Change:         false,
	}

	assetAmount := bc.AssetAmount{
		AssetId: assetId,
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

func GenerateBaseUtxos(num int) ([]*account.UTXO) {
	utxos := []*account.UTXO{}
	for i:= 0; i< num;i++{
		utxo := MockUtxo(uint64(i), consensus.BTMAssetID, 624000000000)
		utxos = append(utxos, utxo)
	}

	return utxos
}

func GenerateOtherUtxos(num int) ([]*account.UTXO) {
	utxos := []*account.UTXO{}

	assetID := &bc.AssetID{
		V0: uint64(18446744073709551615),
		V1: uint64(1),
		V2: uint64(0),
		V3: uint64(1),
	}

	for i:= 0; i< num;i++{
		utxo := MockUtxo(uint64(i), assetID, 6000)
		utxos = append(utxos, utxo)
	}

	return utxos
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

func AddTxBuilder(tplBuilder *txbuilder.TemplateBuilder, utxo *account.UTXO) error {
	txInput, signInst, err := AddTxInputFromUtxo(utxo)
	if err != nil {
		return err
	}
	tplBuilder.AddInput(txInput, signInst)

	txOutput := AddTxOutput(utxo.AssetID, utxo.Amount, utxo.ControlProgram)
	tplBuilder.AddOutput(txOutput)

	return nil
}

func BuildTx(baseUtxo *account.UTXO, otherUtxos []*account.UTXO) (*types.Tx, error) {
	tplBuilder, err := CreateTxBuilder(baseUtxo)
	if err != nil {
		return nil, err
	}

	for _, u := range otherUtxos {
		if err := AddTxBuilder(tplBuilder, u); err != nil {
			return nil, err
		}
	}

	tpl, _, err := tplBuilder.Build()
	if err != nil {
		return nil, err
	}

	return tpl.Transaction, nil
}

func GenetrateTx(baseUtxo []*account.UTXO, otherUtxo []*account.UTXO, num int) ([]*types.Tx, error) {
	if num > len(baseUtxo) || num > len(otherUtxo) {
		return nil, errors.New("utxo is not enough")
	}

	txOtherUtxos := []*account.UTXO{}
	txs := []*types.Tx{}

	for i:= 0; i<num; i++{
		// add other assetID utxo, only one utxo has been inserted into
		txOtherUtxos = append(txOtherUtxos, otherUtxo[i])

		tx, err := BuildTx(baseUtxo[i], txOtherUtxos)
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)

		// reinit txOtherUtxos
		txOtherUtxos = []*account.UTXO{}
	}

	return txs, nil
}

func SetUtxoView(db dbm.DB, view *state.UtxoViewpoint) error {
	batch := db.NewBatch()
	if err := leveldb.SaveUtxoView(batch, view); err != nil {
		return err
	}
	batch.Write()
	return nil
}
