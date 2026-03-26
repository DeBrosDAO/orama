package auth

import "context"

type contextKey string

const (
	walletKey contextKey = "wallet"
	chainKey  contextKey = "chain"
)

func WithWallet(ctx context.Context, wallet, chain string) context.Context {
	ctx = context.WithValue(ctx, walletKey, wallet)
	ctx = context.WithValue(ctx, chainKey, chain)
	return ctx
}

func WalletFromContext(ctx context.Context) (wallet, chain string, ok bool) {
	w, wOk := ctx.Value(walletKey).(string)
	c, cOk := ctx.Value(chainKey).(string)
	if !wOk || !cOk || w == "" || c == "" {
		return "", "", false
	}
	return w, c, true
}
