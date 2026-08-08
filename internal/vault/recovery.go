package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

type DoorEnvelope struct {
	IV string `json:"iv"`
	CT string `json:"ct"`
}

type RecoveryFile struct {
	V      int          `json:"v"`
	Kind   string       `json:"kind"`
	Label  string       `json:"label"`
	Email  string       `json:"email"`
	KDF    KDFMeta      `json:"kdf"`
	Vault  DoorEnvelope `json:"vault"`
	Escrow DoorEnvelope `json:"escrow"`
}

func GenerateRecoveryCode() (string, []byte, error) {
	codeBytes := make([]byte, 32) // 256 bits
	if _, err := io.ReadFull(rand.Reader, codeBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate random recovery code: %w", err)
	}
	codeStr := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(codeBytes)
	return codeStr, codeBytes, nil
}

func DeriveEscrowKey(codeBytes []byte) ([]byte, error) {
	rk, err := hkdf.Key(sha256.New, codeBytes, nil, "recovery", 32)
	if err != nil {
		return nil, fmt.Errorf("failed to derive escrow key: %w", err)
	}
	return rk, nil
}

func EncryptDoor(key []byte, ptBytes []byte) (*DoorEnvelope, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	iv := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	ct := gcm.Seal(nil, iv, ptBytes, []byte(RecoveryAAD))
	return &DoorEnvelope{
		IV: base64.StdEncoding.EncodeToString(iv),
		CT: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

func DecryptDoor(key []byte, door DoorEnvelope) (*VaultPlaintext, error) {
	iv, err := base64.StdEncoding.DecodeString(door.IV)
	if err != nil {
		return nil, fmt.Errorf("invalid IV base64: %w", err)
	}

	ct, err := base64.StdEncoding.DecodeString(door.CT)
	if err != nil {
		return nil, fmt.Errorf("invalid CT base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	ptBytes, err := gcm.Open(nil, iv, ct, []byte(RecoveryAAD))
	if err != nil {
		return nil, fmt.Errorf("GCM authentication failed: %w", err)
	}

	var pt VaultPlaintext
	if err := json.Unmarshal(ptBytes, &pt); err != nil {
		return nil, fmt.Errorf("failed to parse decrypted plaintext: %w", err)
	}

	return &pt, nil
}

func BuildRecoveryFile(email, password string, pt *VaultPlaintext, recoveryCodeBytes []byte) (*RecoveryFile, error) {
	kd, err := DeriveKeys(email, password)
	if err != nil {
		return nil, fmt.Errorf("failed to derive keys for recovery file: %w", err)
	}
	defer kd.Zero()

	escrowKey, err := DeriveEscrowKey(recoveryCodeBytes)
	if err != nil {
		return nil, err
	}
	defer ZeroBytes(escrowKey)

	ptBytes, err := json.Marshal(pt)
	if err != nil {
		return nil, err
	}

	vaultDoor, err := EncryptDoor(kd.KEK, ptBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt password door: %w", err)
	}

	escrowDoor, err := EncryptDoor(escrowKey, ptBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt escrow door: %w", err)
	}

	return &RecoveryFile{
		V:     1,
		Kind:  "1f916-recovery",
		Label: "1f916 recovery backup",
		Email: email,
		KDF: KDFMeta{
			Name: "argon2id",
			M:    262144,
			T:    3,
			P:    1,
		},
		Vault:  *vaultDoor,
		Escrow: *escrowDoor,
	}, nil
}
