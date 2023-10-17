package fuz

import (
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
	"math/rand"
	"testing"
)

func FuzzSDK(f *testing.F) {
	accounts := testutils.RandomAccounts(rand.New(rand.NewSource(1)), 100)

	f.Fuzz(func(t *testing.T, data []byte) {
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

		if len(data) < 26 {
			return
		}

		for i := 0; i < int(data[0])%5; i++ {
			idx := i*4 + 1

			tx, err := testutils.CreateRandomTx(
				encodingConfig.TxConfig,
				accounts[int(data[idx])%len(accounts)],
				uint64(data[idx]),
				uint64(data[idx+1]),
				uint64(data[idx+2]),
				uint64(data[idx+3]),
				sdk.NewCoin("stake", math.NewInt(int64(data[idx+4]))),
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
