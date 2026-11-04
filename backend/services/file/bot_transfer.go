package file

import (
	"context"
	"errors"

	"TDrive/backend/tgclient"
)

var ErrNoTransferBots = errors.New("no transfer bots are configured")

// BotTransferSelector chooses one authenticated bot and resolves the drive
// peer using that bot's account-specific access hash.
type BotTransferSelector interface {
	Select(context.Context, int64) (tgclient.Client, tgclient.InputPeer, string, func(), error)
	SelectBot(context.Context, int64, string) (tgclient.Client, tgclient.InputPeer, func(), error)
}

type botTransferContextKey struct{}

type selectedBotTransfer struct {
	client  tgclient.Client
	peer    tgclient.InputPeer
	id      string
	release func()
}

func withBotTransfer(ctx context.Context, selected selectedBotTransfer) context.Context {
	return context.WithValue(ctx, botTransferContextKey{}, selected)
}

func botTransferFrom(ctx context.Context) (selectedBotTransfer, bool) {
	selected, ok := ctx.Value(botTransferContextKey{}).(selectedBotTransfer)
	return selected, ok && selected.client != nil
}

func (s *Service) transferTG(ctx context.Context) tgclient.Client {
	if selected, ok := botTransferFrom(ctx); ok {
		return selected.client
	}
	return s.TG
}
