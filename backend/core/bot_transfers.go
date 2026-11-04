package core

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"TDrive/backend/auth"
	fileservice "TDrive/backend/services/file"
	"TDrive/backend/tgclient"
)

type botTransfer struct {
	id      string
	client  tgclient.Client
	refs    int
	retired bool
}

// botTransfers owns the MTProto clients used only for file-body transfers.
// Each returned client and peer pair belongs to one bot and must stay pinned
// for the whole transfer, including retries.
type botTransfers struct {
	mu      sync.Mutex
	clients map[string]*botTransfer
	next    atomic.Uint64
}

func newBotTransfers() *botTransfers {
	return &botTransfers{clients: make(map[string]*botTransfer)}
}

func (p *botTransfers) Select(ctx context.Context, channelID int64) (tgclient.Client, tgclient.InputPeer, string, func(), error) {
	accounts, err := auth.ListBotAccounts()
	if err != nil {
		return nil, tgclient.InputPeer{}, "", nil, err
	}
	if len(accounts) == 0 {
		return nil, tgclient.InputPeer{}, "", nil, fileservice.ErrNoTransferBots
	}
	start := int(p.next.Add(1)-1) % len(accounts)
	var lastErr error
	for offset := 0; offset < len(accounts); offset++ {
		account := accounts[(start+offset)%len(accounts)]
		client, release, err := p.acquire(account.ID)
		if err != nil {
			lastErr = err
			continue
		}
		peer, err := resolveBotDriveChannel(ctx, client, channelID)
		if err != nil {
			release()
			lastErr = fmt.Errorf("bot %q cannot access this drive: %w", account.Name, err)
			continue
		}
		return client, peer, account.ID, release, nil
	}
	return nil, tgclient.InputPeer{}, "", nil, fmt.Errorf("no configured transfer bot can access drive %d: %w", channelID, lastErr)
}

func (p *botTransfers) SelectBot(ctx context.Context, channelID int64, id string) (tgclient.Client, tgclient.InputPeer, func(), error) {
	client, release, err := p.acquire(id)
	if err != nil {
		return nil, tgclient.InputPeer{}, nil, err
	}
	peer, err := resolveBotDriveChannel(ctx, client, channelID)
	if err != nil {
		release()
		return nil, tgclient.InputPeer{}, nil, fmt.Errorf("configured transfer bot cannot access this drive: %w", err)
	}
	return client, peer, release, nil
}

func resolveBotDriveChannel(ctx context.Context, client tgclient.Client, channelID int64) (tgclient.InputPeer, error) {
	if resolver, ok := client.(interface {
		ResolveBotDriveChannel(context.Context, int64) (tgclient.InputPeer, error)
	}); ok {
		return resolver.ResolveBotDriveChannel(ctx, channelID)
	}
	return client.ResolveDriveChannel(ctx, channelID)
}

func (p *botTransfers) client(id string) (tgclient.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.clients[id]; entry != nil {
		if entry.retired {
			return nil, fmt.Errorf("transfer bot is being removed")
		}
		return entry.client, nil
	}
	connect, authorize, err := auth.BotClientFactory(id)
	if err != nil {
		return nil, err
	}
	client := tgclient.NewGotdWithAuth(connect, authorize)
	p.clients[id] = &botTransfer{id: id, client: client}
	return client, nil
}

func (p *botTransfers) acquire(id string) (tgclient.Client, func(), error) {
	client, err := p.client(id)
	if err != nil {
		return nil, nil, err
	}
	p.mu.Lock()
	entry := p.clients[id]
	if entry == nil || entry.retired {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("transfer bot is being removed")
	}
	entry.refs++
	p.mu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { p.release(entry) }) }
	return client, release, nil
}

func (p *botTransfers) release(entry *botTransfer) {
	p.mu.Lock()
	entry.refs--
	closeNow := entry.retired && entry.refs == 0
	if closeNow && p.clients[entry.id] == entry {
		delete(p.clients, entry.id)
	}
	p.mu.Unlock()
	if closeNow {
		entry.client.Close()
		auth.RemoveBotSession(entry.id)
	}
}

func (p *botTransfers) CloseBot(id string) {
	p.mu.Lock()
	entry := p.clients[id]
	var client tgclient.Client
	if entry != nil {
		entry.retired = true
		if entry.refs == 0 {
			delete(p.clients, id)
			client = entry.client
		}
	}
	p.mu.Unlock()
	if client != nil {
		client.Close()
		auth.RemoveBotSession(id)
	} else if entry == nil || entry.refs == 0 {
		auth.RemoveBotSession(id)
	}
}

func (p *botTransfers) Close() {
	p.mu.Lock()
	clients := p.clients
	p.clients = make(map[string]*botTransfer)
	p.mu.Unlock()
	for _, entry := range clients {
		entry.client.Close()
	}
}
