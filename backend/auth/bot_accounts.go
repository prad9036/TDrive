package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"TDrive/backend/datadir"

	"github.com/gotd/td/telegram"
)

// BotAccountInfo is safe to expose to the UI: bot tokens never leave this
// package through a returned value.
type BotAccountInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
}

type botAccount struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	Token    string `json:"token"`
}

var botAccountsMu sync.Mutex

func botAccountsPath() (string, error) {
	dir, err := datadir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bot-accounts.json"), nil
}

func loadBotAccounts() ([]botAccount, error) {
	path, err := botAccountsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bot accounts: %w", err)
	}
	var accounts []botAccount
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, fmt.Errorf("decode bot accounts: %w", err)
	}
	for _, account := range accounts {
		if account.ID == "" || account.Token == "" {
			return nil, fmt.Errorf("decode bot accounts: invalid account entry")
		}
	}
	return accounts, nil
}

// ListBotAccounts returns bot labels without exposing their credentials.
func ListBotAccounts() ([]BotAccountInfo, error) {
	accounts, err := loadBotAccounts()
	if err != nil {
		return nil, err
	}
	out := make([]BotAccountInfo, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, BotAccountInfo{ID: account.ID, Name: account.Name, Username: account.Username})
	}
	return out, nil
}

// AddBotAccount stores a Telegram bot token in the private TDrive config
// directory. Re-adding the same token updates its display name.
func AddBotAccount(name, token string) (BotAccountInfo, error) {
	botAccountsMu.Lock()
	defer botAccountsMu.Unlock()
	name = strings.TrimSpace(name)
	token = strings.TrimSpace(token)
	if name == "" || len(name) > 80 {
		return BotAccountInfo{}, fmt.Errorf("bot name must be between 1 and 80 characters")
	}
	if !validBotToken(token) {
		return BotAccountInfo{}, fmt.Errorf("invalid Telegram bot token")
	}
	sum := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(sum[:12])
	accounts, err := loadBotAccounts()
	if err != nil {
		return BotAccountInfo{}, err
	}
	updated := false
	for index := range accounts {
		if accounts[index].ID == id {
			accounts[index].Name = name
			updated = true
			break
		}
	}
	if !updated {
		accounts = append(accounts, botAccount{ID: id, Name: name, Token: token})
	} else {
		for index := range accounts {
			if accounts[index].ID == id {
				accounts[index].Token = token
			}
		}
	}
	if err := saveBotAccounts(accounts); err != nil {
		return BotAccountInfo{}, err
	}
	return BotAccountInfo{ID: id, Name: name}, nil
}

// RemoveBotAccount deletes one configured bot credential. The transfer manager
// removes its MTProto session after any in-flight transfer releases the client.
func RemoveBotAccount(id string) error {
	botAccountsMu.Lock()
	defer botAccountsMu.Unlock()
	if !validBotAccountID(id) {
		return fmt.Errorf("invalid bot account id")
	}
	accounts, err := loadBotAccounts()
	if err != nil {
		return err
	}
	filtered := accounts[:0]
	found := false
	for _, account := range accounts {
		if account.ID == id {
			found = true
			continue
		}
		filtered = append(filtered, account)
	}
	if !found {
		return os.ErrNotExist
	}
	if err := saveBotAccounts(filtered); err != nil {
		return err
	}
	return nil
}

// RemoveBotSession removes the persistent MTProto session after its live client
// has closed, so removing a bot cannot interrupt an in-flight transfer.
func RemoveBotSession(id string) {
	if !validBotAccountID(id) {
		return
	}
	dir, err := datadir.Dir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, "bot-sessions", id+".json"))
}

// BotClientFactory builds one bot's gotd client and auth callback. The token is
// captured inside auth and is never returned to the caller.
func BotClientFactory(id string) (func() (*telegram.Client, error), func(context.Context, *telegram.Client) error, error) {
	if !validBotAccountID(id) {
		return nil, nil, fmt.Errorf("invalid bot account id")
	}
	accounts, err := loadBotAccounts()
	if err != nil {
		return nil, nil, err
	}
	var account *botAccount
	for index := range accounts {
		if accounts[index].ID == id {
			account = &accounts[index]
			break
		}
	}
	if account == nil {
		return nil, nil, os.ErrNotExist
	}
	creds, err := LoadImpCredentials()
	if err != nil {
		return nil, nil, fmt.Errorf("Telegram API credentials are not configured")
	}
	dir, err := datadir.Dir()
	if err != nil {
		return nil, nil, err
	}
	sessionDir := filepath.Join(dir, "bot-sessions")
	if err := os.MkdirAll(sessionDir, privateDirMode); err != nil {
		return nil, nil, err
	}
	_ = os.Chmod(sessionDir, privateDirMode)
	sessionPath := filepath.Join(sessionDir, id+".json")
	connect := func() (*telegram.Client, error) {
		if err := os.Chmod(sessionPath, privateFileMode); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("secure bot session: %w", err)
		}
		return telegram.NewClient(creds.APIID, creds.APIHash, telegram.Options{
			SessionStorage: &privateSessionStorage{path: sessionPath},
		}), nil
	}
	token := account.Token
	authorize := func(ctx context.Context, client *telegram.Client) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return err
		}
		if status.Authorized {
			return nil
		}
		_, err = client.Auth().Bot(ctx, token)
		return err
	}
	return connect, authorize, nil
}

func validBotAccountID(id string) bool {
	if len(id) != 24 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func saveBotAccounts(accounts []botAccount) error {
	path, err := botAccountsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, data)
}

func validBotToken(token string) bool {
	if len(token) < 30 || len(token) > 256 {
		return false
	}
	colon := strings.IndexByte(token, ':')
	if colon < 1 || colon == len(token)-1 {
		return false
	}
	for _, r := range token {
		if !(r == ':' || r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}
