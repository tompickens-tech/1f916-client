package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

const (
	VaultAAD      = "1f916-vault-v1"
	VaultBlobSize = 512
	DraftBlobSize = 4096
	DraftAAD      = "1f916-drafts-v1"
	RecoveryAAD   = "1f916-recovery-v1"
)

type KDFMeta struct {
	Name string `json:"name"`
	M    int    `json:"m"`
	T    int    `json:"t"`
	P    int    `json:"p"`
}

type VaultEnvelope struct {
	V   int     `json:"v"`
	KDF KDFMeta `json:"kdf"`
	IV  string  `json:"iv"`
	CT  string  `json:"ct"`
	Pad string  `json:"pad"`
}

type VaultPlaintext struct {
	V      int    `json:"v"`
	Secret string `json:"secret"`
	Handle string `json:"handle"`
}

func EncryptVaultBlob(kek []byte, plaintext *VaultPlaintext) ([]byte, error) {
	ptBytes, err := json.Marshal(plaintext)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal plaintext: %w", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM mode: %w", err)
	}

	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	ciphertext := gcm.Seal(nil, iv, ptBytes, []byte(VaultAAD))

	env := VaultEnvelope{
		V: 1,
		KDF: KDFMeta{
			Name: "argon2id",
			M:    262144,
			T:    3,
			P:    1,
		},
		IV:  base64.StdEncoding.EncodeToString(iv),
		CT:  base64.StdEncoding.EncodeToString(ciphertext),
		Pad: "",
	}

	// Calculate pad length so total JSON size is exactly VaultBlobSize (512 bytes)
	dummyBytes, _ := json.Marshal(env)
	neededPadLen := VaultBlobSize - len(dummyBytes)
	if neededPadLen < 0 {
		return nil, fmt.Errorf("vault envelope exceeds %d bytes (%d bytes)", VaultBlobSize, len(dummyBytes))
	}

	padBytes := make([]byte, neededPadLen)
	if _, err := io.ReadFull(rand.Reader, padBytes); err != nil {
		return nil, fmt.Errorf("failed to generate pad bytes: %w", err)
	}
	env.Pad = base64.RawURLEncoding.EncodeToString(padBytes)

	// Adjust padding to hit exact 512 byte total size
	finalBytes, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal final envelope: %w", err)
	}

	if len(finalBytes) != VaultBlobSize {
		// Trim or extend pad string to land on exact VaultBlobSize
		diff := VaultBlobSize - len(finalBytes)
		if diff > 0 {
			env.Pad += stringsRepeat("A", diff)
		} else if diff < 0 && len(env.Pad) >= -diff {
			env.Pad = env.Pad[:len(env.Pad)+diff]
		}
		finalBytes, _ = json.Marshal(env)
	}

	if len(finalBytes) != VaultBlobSize {
		return nil, fmt.Errorf("vault blob size assertion failed: got %d, want %d", len(finalBytes), VaultBlobSize)
	}

	return finalBytes, nil
}

func DecryptVaultBlob(kek []byte, blobBytes []byte) (*VaultPlaintext, error) {
	var env VaultEnvelope
	if err := json.Unmarshal(blobBytes, &env); err != nil {
		return nil, fmt.Errorf("unparseable vault envelope JSON: %w", err)
	}

	iv, err := base64.StdEncoding.DecodeString(env.IV)
	if err != nil {
		return nil, fmt.Errorf("invalid IV base64: %w", err)
	}

	ct, err := base64.StdEncoding.DecodeString(env.CT)
	if err != nil {
		return nil, fmt.Errorf("invalid ciphertext base64: %w", err)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM mode: %w", err)
	}

	ptBytes, err := gcm.Open(nil, iv, ct, []byte(VaultAAD))
	if err != nil {
		return nil, fmt.Errorf("GCM authentication failed (corrupted blob or wrong key): %w", err)
	}

	var pt VaultPlaintext
	if err := json.Unmarshal(ptBytes, &pt); err != nil {
		return nil, fmt.Errorf("failed to parse decrypted vault plaintext: %w", err)
	}

	return &pt, nil
}

func stringsRepeat(s string, count int) string {
	if count <= 0 {
		return ""
	}
	out := make([]byte, count*len(s))
	bp := copy(out, s)
	for bp < len(out) {
		copy(out[bp:], out[:bp])
		bp *= 2
	}
	return string(out)
}
