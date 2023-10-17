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

		defaultLane := defaultlane.NewDefaultLane(cfg)

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
			if err = defaultLane.Insert(sdk.Context{}, tx); err != nil {
				t.Errorf("unexpected insertion error")
			}
		}

		mempool := block.NewLanedMempool(logger, false, defaultLane)

		ctx := testutil.DefaultContextWithDB(t, storetypes.NewKVStoreKey("test"), storetypes.NewTransientStoreKey("transient_test")).Ctx.WithIsCheckTx(true)
		ctx = ctx.WithConsensusParams(
			tmprototypes.ConsensusParams{
				Block: &tmprototypes.BlockParams{
					MaxBytes: 1000000000000,
					MaxGas:   1000000000000,
				},
			},
		)

		proposalHandler := abci.NewProposalHandler(
			logger,
			encodingConfig.TxConfig.TxDecoder(),
			encodingConfig.TxConfig.TxEncoder(),
			mempool,
		)
		prepareProposalHandler := proposalHandler.PrepareProposalHandler()
		processProposalHandler := proposalHandler.ProcessProposalHandler()

		resp, err := prepareProposalHandler(ctx, &cometabci.RequestPrepareProposal{Height: 2})
		if err != nil {
			t.Errorf("bad prepare: %q", err)
		}

		resp2, err := processProposalHandler(ctx, &cometabci.RequestProcessProposal{Txs: resp.Txs, Height: 2})
		if err != nil || resp2.Status != cometabci.ResponseProcessProposal_ACCEPT {
			t.Errorf("bad process")
		}

	})
}
