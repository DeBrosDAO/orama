package app_test

import (
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/DeBrosOfficial/network/chain/app/params"
	oramatx "github.com/DeBrosOfficial/network/chain/client/tx"
)

const (
	// benchWalletOrama is the bank balance of each benchmark wallet: enough fees for far more
	// blocks than a benchmark runs.
	benchWalletOrama = int64(100)
	// benchSendNorama is the amount of each benchmark send.
	benchSendNorama = int64(1)
	// benchCheckTxPool is the distinct transactions CheckTx cycles through between empty blocks.
	benchCheckTxPool = 64
)

// benchSender is a funded wallet and the account number its transactions carry.
type benchSender struct {
	wallet  wallet
	account uint64
}

// benchSenders creates n wallets on f's chain, each with a bank balance, and reads their account numbers.
func benchSenders(b *testing.B, f *flow, n int) []benchSender {
	b.Helper()
	senders := make([]benchSender, n)
	for i := range senders {
		w := f.newWallet()
		f.fundBank(w, benchWalletOrama)
		acct := f.app.AccountKeeper.GetAccount(f.app.NewContext(true), w.addr)
		senders[i] = benchSender{wallet: w, account: acct.GetAccountNumber()}
	}
	return senders
}

// signSend signs a public bank send from s to itself with the given sequence, the way a wallet does.
func (f *flow) signSend(b *testing.B, s benchSender, sequence uint64) []byte {
	b.Helper()
	msg := banktypes.NewMsgSend(s.wallet.addr, s.wallet.addr, sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, benchSendNorama)))
	raw, err := f.builder.Build(s.wallet.acc, oramatx.Unsigned{
		ChainID: testChainID, AccountNumber: s.account, Sequence: sequence,
		GasLimit: flowGas, Fee: sdk.NewCoins(sdk.NewInt64Coin(params.BaseDenom, int64(flowGas))), Msgs: []sdk.Msg{msg},
	})
	if err != nil {
		b.Fatal(err)
	}
	return raw
}

// BenchmarkFinalizeBlock is one block executed and committed on the app test harness: empty, and
// carrying 1 and 100 public bank sends from distinct wallets. Building and signing the
// transactions is outside the timing.
func BenchmarkFinalizeBlock(b *testing.B) {
	for _, txs := range []int{0, 1, 100} {
		b.Run(fmt.Sprintf("txs=%d", txs), func(b *testing.B) {
			f := newFlow(b)
			senders := benchSenders(b, f, txs)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				block := make([][]byte, txs)
				for j, s := range senders {
					block[j] = f.signSend(b, s, uint64(i))
				}
				f.height++
				b.StartTimer()
				resp := finalize(b, f.app, f.height, f.at(f.height), block...)
				b.StopTimer()
				for _, r := range resp.TxResults {
					if r.Code != abci.CodeTypeOK {
						b.Fatalf("a benchmark transaction failed: %s", r.Log)
					}
				}
				b.StartTimer()
			}
		})
	}
}

// BenchmarkCheckTx is the mempool admission of one signed public bank send: decode, the ante chain
// (signature, sequence, fee) and the pool bookkeeping. An empty block between rounds of
// benchCheckTxPool transactions resets the check state, so every transaction is accepted afresh.
func BenchmarkCheckTx(b *testing.B) {
	f := newFlow(b)
	senders := benchSenders(b, f, benchCheckTxPool)
	txs := make([][]byte, len(senders))
	for i, s := range senders {
		txs[i] = f.signSend(b, s, 0)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 && i%len(txs) == 0 {
			b.StopTimer()
			f.height++
			finalize(b, f.app, f.height, f.at(f.height))
			b.StartTimer()
		}
		res, err := f.app.CheckTx(&abci.RequestCheckTx{Tx: txs[i%len(txs)], Type: abci.CheckTxType_New})
		if err != nil {
			b.Fatal(err)
		}
		if res.Code != abci.CodeTypeOK {
			b.Fatalf("CheckTx refused a benchmark transaction: %s", res.Log)
		}
	}
}
