package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"TDrive/backend/datadir"
)

const (
	maxP2PReceiveBytes = int64(40 << 30)
	maxP2PChunkBytes   = 64 << 10
)

type p2pReceiveFile struct {
	file     *os.File
	path     string
	name     string
	expected int64
	written  int64
}

// BeginP2PReceive opens a private temporary destination for a direct transfer.
// Chunks are written there as they arrive, so a large receive does not have to
// be retained in the WebView's memory.
func (a *App) BeginP2PReceive(name string, size int64) (string, error) {
	if a == nil {
		return "", errBackendUnavailable
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "received-file"
	}
	if size < 0 || size > maxP2PReceiveBytes {
		return "", fmt.Errorf("direct transfers are limited to %d GiB", maxP2PReceiveBytes>>30)
	}
	file, err := datadir.CreateCacheTemp("tdrive-p2p-*.part")
	if err != nil {
		return "", fmt.Errorf("create temporary receive file: %w", err)
	}
	var randomID [16]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return "", fmt.Errorf("create transfer id: %w", err)
	}
	token := hex.EncodeToString(randomID[:])
	a.p2pReceiveMu.Lock()
	if a.p2pReceives == nil {
		a.p2pReceives = make(map[string]*p2pReceiveFile)
	}
	a.p2pReceives[token] = &p2pReceiveFile{file: file, path: file.Name(), name: name, expected: size}
	a.p2pReceiveMu.Unlock()
	return token, nil
}

// WriteP2PReceiveChunk appends one bounded base64 chunk to its active receive.
func (a *App) WriteP2PReceiveChunk(token, encoded string) error {
	if len(encoded) > base64.StdEncoding.EncodedLen(maxP2PChunkBytes) {
		return fmt.Errorf("direct transfer chunk exceeds %d KiB", maxP2PChunkBytes>>10)
	}
	chunk, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(chunk) == 0 || len(chunk) > maxP2PChunkBytes {
		return fmt.Errorf("invalid direct transfer chunk")
	}
	a.p2pReceiveMu.Lock()
	defer a.p2pReceiveMu.Unlock()
	receive := a.p2pReceives[token]
	if receive == nil {
		return fmt.Errorf("direct transfer is no longer active")
	}
	if receive.written+int64(len(chunk)) > receive.expected {
		return fmt.Errorf("received data exceeds the announced file size")
	}
	n, err := receive.file.Write(chunk)
	receive.written += int64(n)
	if err != nil {
		return fmt.Errorf("write direct transfer data: %w", err)
	}
	if n != len(chunk) {
		return io.ErrShortWrite
	}
	return nil
}

// FinishP2PReceive publishes a complete temporary receive through the
// platform's normal save path (desktop dialog or mobile Downloads directory).
func (a *App) FinishP2PReceive(token string) (string, error) {
	a.p2pReceiveMu.Lock()
	receive := a.p2pReceives[token]
	if receive == nil {
		a.p2pReceiveMu.Unlock()
		return "", fmt.Errorf("direct transfer is no longer active")
	}
	delete(a.p2pReceives, token)
	a.p2pReceiveMu.Unlock()
	defer os.Remove(receive.path)
	if receive.written != receive.expected {
		_ = receive.file.Close()
		return "", fmt.Errorf("received %d of %d file bytes", receive.written, receive.expected)
	}
	if err := receive.file.Sync(); err != nil {
		_ = receive.file.Close()
		return "", fmt.Errorf("sync received file: %w", err)
	}
	if err := receive.file.Close(); err != nil {
		return "", fmt.Errorf("close received file: %w", err)
	}
	target, err := a.chooseDownloadPath(receive.name)
	if err != nil {
		return "", fmt.Errorf("choose save location: %w", err)
	}
	return publishP2PReceive(receive.path, target)
}

// CancelP2PReceive abandons an incomplete peer receive and removes its staging file.
func (a *App) CancelP2PReceive(token string) error {
	a.p2pReceiveMu.Lock()
	receive := a.p2pReceives[token]
	delete(a.p2pReceives, token)
	a.p2pReceiveMu.Unlock()
	if receive == nil {
		return nil
	}
	closeErr := receive.file.Close()
	removeErr := os.Remove(receive.path)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}

func (a *App) closeP2PReceives() {
	a.p2pReceiveMu.Lock()
	receives := a.p2pReceives
	a.p2pReceives = make(map[string]*p2pReceiveFile)
	a.p2pReceiveMu.Unlock()
	for _, receive := range receives {
		_ = receive.file.Close()
		_ = os.Remove(receive.path)
	}
}

func publishP2PReceive(source, target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("save location is empty")
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".tdrive-p2p-*.part")
	if err != nil {
		return "", fmt.Errorf("create destination staging file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	input, err := os.Open(source)
	if err != nil {
		_ = temp.Close()
		return "", err
	}
	_, copyErr := io.Copy(temp, input)
	inputErr := input.Close()
	if copyErr != nil {
		_ = temp.Close()
		return "", fmt.Errorf("copy received file to destination: %w", copyErr)
	}
	if inputErr != nil {
		_ = temp.Close()
		return "", inputErr
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err == nil {
		backupFile, err := os.CreateTemp(filepath.Dir(target), ".tdrive-p2p-backup-*")
		if err != nil {
			return "", err
		}
		backup := backupFile.Name()
		if err := backupFile.Close(); err != nil {
			_ = os.Remove(backup)
			return "", err
		}
		if err := os.Remove(backup); err != nil {
			return "", err
		}
		if err := os.Rename(target, backup); err != nil {
			return "", err
		}
		if err := os.Rename(tempPath, target); err != nil {
			return "", errors.Join(err, os.Rename(backup, target))
		}
		if err := os.Remove(backup); err != nil {
			return target, fmt.Errorf("file saved, but the previous-copy cleanup failed: %w", err)
		}
		return target, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Rename(tempPath, target); err != nil {
		return "", err
	}
	return target, nil
}
