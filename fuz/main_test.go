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
	account testutils.Account
	accountIdx int64
	nonce uint8
	numberMsgs uint8
	timeout uint64
	gasLimit uint64
	fees uint64
}

func FuzzReverse(f *testing.F) {
	accounts := testutils.RandomAccounts(rand.New(rand.NewSource(1)), ACCT_CNT)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < int(unsafe.Sizeof(DummyTx{})) * TX_CNT {
			return
		}
		fuzzConsumer := fuzz.NewConsumer(data)
		acct := make([]DummyTx, TX_CNT)
		for i := 0; i < TX_CNT; i++ {
			err := fuzzConsumer.GenerateStruct(&acct[i])
			if err != nil {
				return
			}
			t.Log(acct[i])
			acct[i].account = accounts[acct[i].accountIdx % ACCT_CNT]
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
				acct[i].account,
				//uint64(i),
				uint64(acct[i].nonce),
				uint64(acct[i].numberMsgs),
				acct[i].timeout,
				acct[i].gasLimit,
				sdk.NewCoin("stake", math.NewInt(int64(i))),
				//sdk.NewCoin("stake", math.NewInt(int64(acct[i].fees))),
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
