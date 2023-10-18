package fuz

import (
	"errors"
	"context"
	"unsafe"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	cometabci "github.com/cometbft/cometbft/abci/types"
	tmprototypes "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	"github.com/skip-mev/block-sdk/abci"
	signeradaptors "github.com/skip-mev/block-sdk/adapters/signer_extraction_adapter"
	"github.com/skip-mev/block-sdk/block"
	"github.com/skip-mev/block-sdk/block/base"
	testutils "github.com/skip-mev/block-sdk/testutils"
	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"time"
	"math/rand"
	"testing"
	"reflect"
)

const (
	ACCT_CNT = 5
	TX_CNT = 5
	VARIATIONS = 2
)

type DummyTx struct {
	accountIdx uint16
	nonce uint16
	numberMsgs uint16
	timeout uint16
	gasLimit uint16
	fees uint16
}

type DummyPriority struct {
	priority uint16
}

//Allow easily manipulation of tx priorities
type DynamicContext struct {
	DynamicPriority map[string]map[uint64]uint16
}

type DynamicPriorityLane struct {
	*base.BaseLane
}

func getTxSenderInfo(tx sdk.Tx) (string, uint64, error) {
	signers, err := signeradaptors.NewDefaultAdapter().GetSigners(tx)
	if err != nil || len(signers) == 0 {
		return "", 0, errors.New("fetch signer failed")
	}
	signer := signers[0]
	return signer.Signer.String(), signer.Sequence, nil
}

func DynamicTxPriority(ctx DynamicContext, t *testing.T) base.TxPriority[uint16] {
	return base.TxPriority[uint16]{
		GetTxPriority: func(goCtx context.Context, tx sdk.Tx) uint16 {
			sender, sequence, err := getTxSenderInfo(tx)
			if err != nil {
				t.Errorf("unexpected getTxSenderInfo error")
				return 0
			}
			senderPriorities, ok := ctx.DynamicPriority[sender]
			if !ok {
				t.Errorf("unexpected get dynamicPriority error (sender)")
				return 0
			}
			priority, ok := senderPriorities[sequence]
			if !ok {
				t.Errorf("unexpected get dynamicPriority error (sequence)")
				return 0
			}
			return priority
		},
		Compare: func(a, b uint16) int {
			if a == b {
				return 0
			} else if a > b {
				return 1
			} else {
				return -1
			}
		},
		MinValue: 0,
	}
}

func NewDynamicPriorityLane(cfg base.LaneConfig, ctx DynamicContext, t *testing.T) *DynamicPriorityLane {
	lane := base.NewBaseLane(
		cfg,
		"dynamic",
		base.NewMempool[uint16](
			DynamicTxPriority(ctx, t),
			cfg.TxEncoder,
			cfg.SignerExtractor,
			cfg.MaxTxs,
		),
		base.DefaultMatchHandler(),
	)

	if err := lane.ValidateBasic(); err != nil {
		panic(err)
	}

	return &DynamicPriorityLane{
		BaseLane: lane,
	}
}

func FuzzReverse(f *testing.F) {
	rand.Seed(time.Now().UnixNano())
	dummyTxSize := int(unsafe.Sizeof(DummyTx{}))
	dummyPrioritySize := int(unsafe.Sizeof(DummyPriority{}))
	accounts := testutils.RandomAccounts(rand.New(rand.NewSource(1)), ACCT_CNT)
	seed := make([]byte, dummyTxSize * TX_CNT + dummyPrioritySize * TX_CNT * VARIATIONS)
	rand.Read(seed)
	f.Add(seed)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < dummyTxSize * TX_CNT {
			return
		}
		acct := [TX_CNT]DummyTx{}
		priorities := [TX_CNT][VARIATIONS]DummyPriority{}
		fuzzConsumer := fuzz.NewConsumer(data)
		for i := 0; i < TX_CNT; i++ {
			acct[i].accountIdx, _ = fuzzConsumer.GetUint16()
			acct[i].nonce, _ = fuzzConsumer.GetUint16()
			acct[i].numberMsgs, _ = fuzzConsumer.GetUint16()
			acct[i].timeout, _ = fuzzConsumer.GetUint16()
			acct[i].gasLimit, _ = fuzzConsumer.GetUint16()
			acct[i].fees, _ = fuzzConsumer.GetUint16()
			for j := 0; j < VARIATIONS; j++ {
				priorities[i][j].priority, _ = fuzzConsumer.GetUint16()
			}
		}
		encodingConfig := testutils.CreateTestEncodingConfig()

		logger := log.NewNopLogger()

		cfg := base.LaneConfig{
			Logger:          logger,
			TxEncoder:       encodingConfig.TxConfig.TxEncoder(),
			TxDecoder:       encodingConfig.TxConfig.TxDecoder(),
			MaxBlockSpace:   sdkmath.LegacyMustNewDecFromStr("1"),
			SignerExtractor: signeradaptors.NewDefaultAdapter(),
		}

		txs := make([]authsigning.Tx, TX_CNT)

		for i := 0; i < TX_CNT; i++ {
			tx, err := testutils.CreateRandomTx(
				encodingConfig.TxConfig,
				accounts[acct[i].accountIdx % ACCT_CNT],
				uint64(acct[i].nonce),
				uint64(acct[i].numberMsgs),
				uint64(acct[i].timeout),
				uint64(acct[i].gasLimit),
				sdk.NewCoin("stake", sdkmath.NewInt(int64(acct[i].fees))),
			)
			if err != nil {
				continue
			}
			txs[i] = tx
		}

		resps := make([]*cometabci.ResponsePrepareProposal, VARIATIONS)
		for i := 0; i < VARIATIONS; i++ {
			//We reconstruct lane from scratch each time to prevent removeTx in prepareProposalHandler from messing up mempool
			dctx := DynamicContext {
				make(map[string]map[uint64]uint16),
			}
			dynamicLane := NewDynamicPriorityLane(cfg, dctx, t)
			mempool := block.NewLanedMempool(logger, false, dynamicLane)
			ctx := testutil.DefaultContextWithDB(
				t,
				storetypes.NewKVStoreKey("test"), 
				storetypes.NewTransientStoreKey("transient_test1"),
			).Ctx.WithIsCheckTx(true)
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
			for j := 0; j < TX_CNT; j++ {
				sender, sequence, err := getTxSenderInfo(txs[j])
				if err != nil {
					t.Errorf("unexpected tx sender info error")
				}
				senderPriority, ok := dctx.DynamicPriority[sender]
				if !ok {
					dctx.DynamicPriority[sender] = make(map[uint64]uint16)
					senderPriority, _ = dctx.DynamicPriority[sender]
				}
				senderPriority[sequence] = priorities[j][0].priority
				if dynamicLane.Insert(ctx, txs[j]) != nil {
					t.Errorf("unexpected insertion error")
				}
				//change priority between Insert and Select (this is no-op first iter)
				dctx.DynamicPriority[sender][sequence] = priorities[j][i].priority
			}
			resp, err := prepareProposalHandler(ctx, &cometabci.RequestPrepareProposal{Height: 2})
			if err != nil {
				t.Errorf("prepare proposal failed")
			}
			resps[i] = resp
		}
		for i := 1; i < VARIATIONS; i++ {
			if !reflect.DeepEqual(resps[0].Txs, resps[i].Txs) {
				for j := 0; j < len(resps[0].Txs); j++ {
					t.Log(encodingConfig.TxConfig.TxDecoder()(resps[0].Txs[j]))
				}
				for j := 0; j < len(resps[i].Txs); j++ {
					t.Log(encodingConfig.TxConfig.TxDecoder()(resps[i].Txs[j]))
				}
				t.Errorf("proposal mismatched")
			}
		}
	})
}
