package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	keySize       = 32
	formatVersion = byte(1)
)

var ErrUnavailable = errors.New("instance secret key is unavailable")

type Manager struct {
	aead      cipher.AEAD
	statusErr error
}

func Open(dataDir string, encryptedSecretCount int) (*Manager, error) {
	directory := filepath.Join(dataDir, "secrets")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create secret directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect secret directory: %w", err)
	}

	path := filepath.Join(directory, "instance.key")
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if encryptedSecretCount > 0 {
			return unavailable(fmt.Errorf("%w: %s is missing", ErrUnavailable, path)), nil
		}
		key, err = createKey(path)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		if encryptedSecretCount > 0 {
			return unavailable(fmt.Errorf("%w: read %s: %v", ErrUnavailable, path, err)), nil
		}
		return nil, fmt.Errorf("read instance key: %w", err)
	}
	if len(key) != keySize {
		return unavailable(fmt.Errorf("%w: %s has invalid length", ErrUnavailable, path)), nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("protect instance key: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret AEAD: %w", err)
	}
	return &Manager{aead: aead}, nil
}

func unavailable(err error) *Manager {
	return &Manager{statusErr: err}
}

func createKey(path string) ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate instance key: %w", err)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil, fmt.Errorf("read concurrently created instance key: %w", readErr)
			}
			return existing, nil
		}
		return nil, fmt.Errorf("create instance key: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(key); err != nil {
		file.Close()
		return nil, fmt.Errorf("write instance key: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("sync instance key: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close instance key: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("open secret directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return nil, fmt.Errorf("sync secret directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return nil, fmt.Errorf("close secret directory: %w", err)
	}
	complete = true
	return key, nil
}

func (manager *Manager) Available() bool {
	return manager != nil && manager.aead != nil && manager.statusErr == nil
}

func (manager *Manager) Status() error {
	if manager == nil {
		return ErrUnavailable
	}
	return manager.statusErr
}

func (manager *Manager) Verify(ownerType, ownerID, name string, ciphertext []byte) error {
	if !manager.Available() {
		return manager.Status()
	}
	if _, err := manager.Decrypt(ownerType, ownerID, name, ciphertext); err != nil {
		manager.aead = nil
		manager.statusErr = fmt.Errorf("%w: stored ciphertext authentication failed", ErrUnavailable)
		return manager.statusErr
	}
	return nil
}

func DiscardKey(dataDir string) error {
	path := filepath.Join(dataDir, "secrets", "instance.key")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("discard unusable instance key: %w", err)
	}
	return nil
}

func (manager *Manager) Encrypt(ownerType, ownerID, name string, plaintext []byte) ([]byte, error) {
	if !manager.Available() {
		return nil, ErrUnavailable
	}
	nonce := make([]byte, manager.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate secret nonce: %w", err)
	}
	result := make([]byte, 1, 1+len(nonce)+len(plaintext)+manager.aead.Overhead())
	result[0] = formatVersion
	result = append(result, nonce...)
	result = manager.aead.Seal(result, nonce, plaintext, associatedData(ownerType, ownerID, name))
	return result, nil
}

func (manager *Manager) Decrypt(ownerType, ownerID, name string, ciphertext []byte) ([]byte, error) {
	if !manager.Available() {
		return nil, ErrUnavailable
	}
	nonceSize := manager.aead.NonceSize()
	if len(ciphertext) < 1+nonceSize+manager.aead.Overhead() || ciphertext[0] != formatVersion {
		return nil, fmt.Errorf("decrypt secret: invalid ciphertext format")
	}
	nonce := ciphertext[1 : 1+nonceSize]
	plaintext, err := manager.aead.Open(nil, nonce, ciphertext[1+nonceSize:], associatedData(ownerType, ownerID, name))
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: authentication failed")
	}
	return plaintext, nil
}

func associatedData(ownerType, ownerID, name string) []byte {
	return []byte(ownerType + "\x00" + ownerID + "\x00" + name)
}
