package fuz

import (
	"unsafe"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	cometabci "github.com/cometbft/cometbft/abci/types"
	tmprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/skip-mev/block-sdk/abci"
	signeradaptors "github.com/skip-mev/block-sdk/adapters/signer_extraction_adapter"
	"github.com/skip-mev/block-sdk/block"
	"github.com/skip-mev/block-sdk/block/base"
	defaultlane "github.com/skip-mev/block-sdk/lanes/base"
	testutils "github.com/skip-mev/block-sdk/testutils"
	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"math/rand"
	"testing"
	"reflect"
)

const (
	ACCT_CNT = 5
	TX_CNT = 5
)

type DummyTx struct {
	accountIdx uint16
	nonce uint16
	numberMsgs uint16
	timeout uint16
	gasLimit uint16
	fees uint16
}

func FuzzReverse(f *testing.F) {
	dummyTxSize := int(unsafe.Sizeof(DummyTx{}))
	accounts := testutils.RandomAccounts(rand.New(rand.NewSource(1)), ACCT_CNT)
	seed := make([]byte, dummyTxSize * TX_CNT)
	rand.Read(seed)
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < dummyTxSize * TX_CNT {
			return
		}
		acct := make([]DummyTx, TX_CNT)
		fuzzConsumer := fuzz.NewConsumer(data)
		for i := 0; i < TX_CNT; i++ {
			//fuzzConsumer.GenerateStruct(&acct[i])
			acct[i].accountIdx, _ = fuzzConsumer.GetUint16()
			acct[i].nonce, _ = fuzzConsumer.GetUint16()
			acct[i].numberMsgs, _ = fuzzConsumer.GetUint16()
			acct[i].timeout, _ = fuzzConsumer.GetUint16()
			acct[i].gasLimit, _ = fuzzConsumer.GetUint16()
			acct[i].fees, _ = fuzzConsumer.GetUint16()
		}
		encodingConfig := testutils.CreateTestEncodingConfig()

		logger := log.NewNopLogger()

		cfg := base.LaneConfig{
			Logger:          logger,
			TxEncoder:       encodingConfig.TxConfig.TxEncoder(),
			TxDecoder:       encodingConfig.TxConfig.TxDecoder(),
			MaxBlockSpace:   math.LegacyMustNewDecFromStr("1"),
			SignerExtractor: signeradaptors.NewDefaultAdapter(),
		}

		//Sort in priority nonce should be strictly ordered and deterministic
		//This means that two copies of lanes should always produce same ordering when same txs are inserted
		//(should also not depend on insertion order, since we require strict ordering and no ties after all tiebreakers are applied)

		defaultLane1 := defaultlane.NewDefaultLane(cfg)
		defaultLane2 := defaultlane.NewDefaultLane(cfg)


		//one exception that makes insertion order matter is when two txs from same sender with same sequence is sent
		//the latter one will override previous one in this case, which we want to avoid, so adjust sequence here for simplicity
		var seen map[uint16]map[uint16]bool = make(map[uint16]map[uint16]bool)
		for i := 0; i < TX_CNT; i++ {
			acct[i].accountIdx %= ACCT_CNT
			idxMap, ok := seen[acct[i].accountIdx]
			if !ok {
				seen[acct[i].accountIdx] = make(map[uint16]bool)
				idxMap = seen[acct[i].accountIdx]
			}
			if _, ok = idxMap[acct[i].nonce]; ok {
				for k := 0; k <= 65535; k++ {
					if _, ok = idxMap[uint16(k)]; !ok {
						acct[i].nonce = uint16(k)
						break
					}
				}
			}
			idxMap[acct[i].nonce] = true
		}

		for i := 0; i < TX_CNT; i++ {
			tx, err := testutils.CreateRandomTx(
				encodingConfig.TxConfig,
				accounts[acct[i].accountIdx % ACCT_CNT],
				uint64(acct[i].nonce),
				uint64(acct[i].numberMsgs),
				uint64(acct[i].timeout),
				uint64(acct[i].gasLimit),
				sdk.NewCoin("stake", math.NewInt(int64(acct[i].fees))),
			)
			if err != nil {
				continue
			}
			if err = defaultLane1.Insert(sdk.Context{}, tx); err != nil {
				t.Errorf("unexpected insertion error")
			}
		}

		for i := TX_CNT - 1; i >= 0; i-- {
			tx, err := testutils.CreateRandomTx(
				encodingConfig.TxConfig,
				accounts[acct[i].accountIdx % ACCT_CNT],
				uint64(acct[i].nonce),
				uint64(acct[i].numberMsgs),
				uint64(acct[i].timeout),
				uint64(acct[i].gasLimit),
				sdk.NewCoin("stake", math.NewInt(int64(acct[i].fees))),
			)
			if err != nil {
				continue
			}
			if err = defaultLane2.Insert(sdk.Context{}, tx); err != nil {
				t.Errorf("unexpected insertion error")
			}
		}

		mempool1 := block.NewLanedMempool(logger, false, defaultLane1)
		mempool2 := block.NewLanedMempool(logger, false, defaultLane2)

		ctx1 := testutil.DefaultContextWithDB(t, storetypes.NewKVStoreKey("test1"), storetypes.NewTransientStoreKey("transient_test1")).Ctx.WithIsCheckTx(true)
		ctx1 = ctx1.WithConsensusParams(
			tmprototypes.ConsensusParams{
				Block: &tmprototypes.BlockParams{
					MaxBytes: 1000000000000,
					MaxGas:   1000000000000,
				},
			},
		)
		ctx2 := testutil.DefaultContextWithDB(t, storetypes.NewKVStoreKey("test2"), storetypes.NewTransientStoreKey("transient_test2")).Ctx.WithIsCheckTx(true)
		ctx2 = ctx2.WithConsensusParams(
			tmprototypes.ConsensusParams{
				Block: &tmprototypes.BlockParams{
					MaxBytes: 1000000000000,
					MaxGas:   1000000000000,
				},
			},
		)

		proposalHandler1 := abci.NewProposalHandler(
			logger,
			encodingConfig.TxConfig.TxDecoder(),
			encodingConfig.TxConfig.TxEncoder(),
			mempool1,
		)
		proposalHandler2 := abci.NewProposalHandler(
			logger,
			encodingConfig.TxConfig.TxDecoder(),
			encodingConfig.TxConfig.TxEncoder(),
			mempool2,
		)
		prepareProposalHandler1 := proposalHandler1.PrepareProposalHandler()
		prepareProposalHandler2 := proposalHandler2.PrepareProposalHandler()

		resp1, prepErr1 := prepareProposalHandler1(ctx1, &cometabci.RequestPrepareProposal{Height: 2})
		if prepErr1 != nil {
			t.Errorf("bad prepare: %q", prepErr1)
		}
		resp2, prepErr2 := prepareProposalHandler2(ctx2, &cometabci.RequestPrepareProposal{Height: 2})
		if prepErr2 != nil {
			t.Errorf("bad prepare: %q", prepErr2)
		}
		if len(resp1.Txs) != len(resp2.Txs) {
			t.Errorf("proposal mismatched")
		}
		for i := 0; i < len(resp1.Txs); i++ {
		}
		if !reflect.DeepEqual(resp1.Txs, resp2.Txs) {
			for i := 0; i < len(resp1.Txs); i++ {
				t.Log(encodingConfig.TxConfig.TxDecoder()(resp1.Txs[i]))
			}
			for i := 0; i < len(resp2.Txs); i++ {
				t.Log(encodingConfig.TxConfig.TxDecoder()(resp2.Txs[i]))
			}
			t.Errorf("proposal mismatched")
		}
	})
}
