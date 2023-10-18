package fuz

import (
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
	defaultlane "github.com/skip-mev/block-sdk/lanes/base"
	testutils "github.com/skip-mev/block-sdk/testutils"
	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"time"
	"math"
	"math/rand"
	"testing"
	"reflect"
)

const (
	ACCT_CNT = 5
	TX_CNT = 5
	COPIES = 2
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
	rand.Seed(time.Now().UnixNano())
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
			MaxBlockSpace:   sdkmath.LegacyMustNewDecFromStr("1"),
			SignerExtractor: signeradaptors.NewDefaultAdapter(),
		}

		//Sort in priority nonce should be strictly ordered and deterministic
		//This means that two copies of lanes should always produce same ordering when same txs are inserted
		//(should also not depend on insertion order, since we require strict ordering and no ties after all tiebreakers are applied)
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
				for k := 0; k <= math.MaxUint16; k++ {
					if _, ok = idxMap[uint16(k)]; !ok {
						acct[i].nonce = uint16(k)
						break
					}
				}
			}
			idxMap[acct[i].nonce] = true
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

		resps := make([]*cometabci.ResponsePrepareProposal, COPIES)

		for i := 0; i < COPIES; i++ {
			defaultLane := defaultlane.NewDefaultLane(cfg)
			mempool := block.NewLanedMempool(logger, false, defaultLane)
			rand.Shuffle(TX_CNT, func(j, k int) {
				txs[j], txs[k] = txs[k], txs[j]
			})
			for j := 0; j < TX_CNT; j++ {
				if err := defaultLane.Insert(sdk.Context{}, txs[j]); err != nil {
					t.Errorf("unexpected insertion error")
				}
			}
			ctx := testutil.DefaultContextWithDB(t, storetypes.NewKVStoreKey("test"), storetypes.NewTransientStoreKey("transient_test1")).Ctx.WithIsCheckTx(true)
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
			resp, err := prepareProposalHandler(ctx, &cometabci.RequestPrepareProposal{Height: 2})
			if err != nil {
				t.Errorf("prepare proposal failed")
			}
			resps[i] = resp
		}
		for i := 1; i < COPIES; i++ {
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
