package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ─── Envrypt (Environment Encryption) ──────────────────────────────────────────

func EnvryptEncrypt(value, key string) string {
	if key == "" || value == "" {
		return ""
	}
	encoded := make([]byte, len(value))
	for i := 0; i < len(value); i++ {
		encoded[i] = value[i] ^ key[i%len(key)]
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func EnvryptDecrypt(value, key string) string {
	if key == "" || value == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return value // fallback if not base64
	}
	decrypted := make([]byte, len(decoded))
	for i := 0; i < len(decoded); i++ {
		decrypted[i] = decoded[i] ^ key[i%len(key)]
	}
	return string(decrypted)
}

func getEnvcryptKey() string {
	key := os.Getenv("ENVRYPT_KEY")
	if key == "" {
		key = os.Getenv("ENVCRYPT_KEY")
	}
	if key == "" {
		data, err := os.ReadFile(".envrypt")
		if err == nil {
			key = strings.TrimSpace(string(data))
		}
	}
	return key
}

// LoadEnv loads .env and decrypts any values under "# encrypted" using the envrypt key.
func LoadEnv(envPath string) error {
	data, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}

	key := getEnvcryptKey()
	lines := strings.Split(string(data), "\n")
	encryptedNext := false
	envKeyRe := regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			encryptedNext = false
			continue
		}
		if strings.ToLower(line) == "# encrypted" {
			encryptedNext = true
			continue
		}

		if match := envKeyRe.FindStringSubmatch(line); match != nil {
			k := match[1]
			v := match[2]

			// Remove quotes if present
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[0] == v[len(v)-1] {
				v = v[1 : len(v)-1]
			}

			if encryptedNext {
				if key == "" {
					return errors.New("encrypted env values found, but no envrypt key was provided")
				}
				v = EnvryptDecrypt(v, key)
			}
			os.Setenv(k, v)
			encryptedNext = false
		} else {
			encryptedNext = false
		}
	}
	return nil
}

// ─── AES-256-GCM Wallet Encryption ─────────────────────────────────────────────

// DecryptWallet decrypts wallet.enc using the provided password.
// Format: IV (12 bytes) + AuthTag (16 bytes) + Encrypted Data
func DecryptWallet(encPath, password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("password not provided")
	}

	encData, err := os.ReadFile(encPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", encPath, err)
	}

	if len(encData) < 28 {
		return nil, errors.New("encrypted file is too short to be valid")
	}

	iv := encData[:12]
	authTag := encData[12:28]
	ciphertext := encData[28:]

	key := sha256.Sum256([]byte(password))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Go's GCM expects ciphertext to be: encryptedData + authTag
	// We need to reorder our buffer because node.js format was IV + AuthTag + EncryptedData
	combined := append(ciphertext, authTag...)

	plaintext, err := aesGCM.Open(nil, iv, combined, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong password?): %w", err)
	}

	return plaintext, nil
}

// EncryptWallet encrypts wallet.json to wallet.enc using the provided password.
func EncryptWallet(walletPath, encPath, password string) error {
	if password == "" {
		return errors.New("password not provided")
	}

	plaintext, err := os.ReadFile(walletPath)
	if err != nil {
		return err
	}

	key := sha256.Sum256([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}

	iv := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}

	sealed := aesGCM.Seal(nil, iv, plaintext, nil)

	// sealed is ciphertext + authTag (16 bytes). We need to split it.
	if len(sealed) < 16 {
		return errors.New("sealed data too short")
	}

	ciphertext := sealed[:len(sealed)-16]
	authTag := sealed[len(sealed)-16:]

	output := append(iv, authTag...)
	output = append(output, ciphertext...)

	// Create dir if needed
	if dir := filepath.Dir(encPath); dir != "." {
		os.MkdirAll(dir, 0755)
	}

	return os.WriteFile(encPath, output, 0600)
}
