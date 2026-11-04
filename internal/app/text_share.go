package app

import (
	"context"
	"fmt"
	"time"
	"unicode/utf16"

	"TDrive/backend/core"
	"TDrive/backend/tgclient"
)

const maxSavedMessagesDraftRunes = 4096

// TextShareService reads and updates the current user's Telegram Saved
// Messages draft. It never sends a message.
type TextShareService struct {
	host serviceHost
}

func newTextShareService(host serviceHost) *TextShareService {
	return &TextShareService{host: host}
}

func (s *TextShareService) client() (context.Context, tgclient.Client, context.CancelFunc, error) {
	if s == nil || s.host == nil {
		return nil, nil, nil, errBackendUnavailable
	}
	ctx := s.host.appContext()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := s.host.WaitForBackendReady(ctx); err != nil {
		cancel()
		return nil, nil, nil, err
	}
	var engine *core.Engine
	if engine = s.host.coreEngine(); engine == nil || engine.Telegram() == nil {
		cancel()
		return nil, nil, nil, errBackendUnavailable
	}
	return ctx, engine.Telegram(), cancel, nil
}

// GetSavedMessagesDraft returns the draft currently stored in the user's
// Telegram Saved Messages conversation.
func (s *TextShareService) GetSavedMessagesDraft() (string, error) {
	ctx, client, cancel, err := s.client()
	if err != nil {
		return "", err
	}
	defer cancel()
	return client.SavedMessagesDraft(ctx)
}

// SaveSavedMessagesDraft updates the cloud draft without sending it.
func (s *TextShareService) SaveSavedMessagesDraft(text string) error {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	if length > maxSavedMessagesDraftRunes {
		return fmt.Errorf("text exceeds Telegram's %d-character draft limit", maxSavedMessagesDraftRunes)
	}
	ctx, client, cancel, err := s.client()
	if err != nil {
		return err
	}
	defer cancel()
	return client.SaveSavedMessagesDraft(ctx, text)
}
