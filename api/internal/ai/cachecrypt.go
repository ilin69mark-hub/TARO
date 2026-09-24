// Шифрование PII в Redis-кэше (см. аудит C: ai:cache лежал plaintext 7 суток).
// AES-256-GCM, ключ из REDIS_ENC_KEY (hex 64 символа). Ключ не задан — plaintext
// (dev/legacy); sealed-значение без ключа считается промахом, не ошибкой.
package ai

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

const encPrefix = "v1:"

// encKey парсит REDIS_ENC_KEY (hex 32 байта). Пусто/мусор — nil (режим plaintext).
func encKey() []byte {
	raw := strings.TrimSpace(os.Getenv("REDIS_ENC_KEY"))
	if raw == "" {
		return nil
	}
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != 32 {
		return nil
	}
	return b
}

// sealCache шифрует текст для Redis. Без ключа — как есть.
func sealCache(plain string) (string, error) {
	key := encKey()
	if key == nil {
		return plain, nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.RawStdEncoding.EncodeToString(ct), nil
}

// openCache расшифровывает значение из Redis. Пусто = промах (не ошибка).
func openCache(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil // legacy plaintext
	}
	key := encKey()
	if key == nil {
		return "", fmt.Errorf("sealed without key")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("short ciphertext")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
