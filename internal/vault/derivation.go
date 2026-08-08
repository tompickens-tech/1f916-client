package vault

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

var derivationMutex sync.Mutex

// ZeroBytes overwrites slice with zeros.
// Note: Go's garbage collector makes memory zeroing best-effort rather than a guarantee.
func ZeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

type KeyDerivation struct {
	Seed    []byte // 32 bytes
	Locator string // 32 hex chars (128 bits)
	KEK     []byte // 32 bytes
}

func (kd *KeyDerivation) Zero() {
	if kd.Seed != nil {
		ZeroBytes(kd.Seed)
	}
	if kd.KEK != nil {
		ZeroBytes(kd.KEK)
	}
}

// DeriveKeys implements exact 1f916 key derivation:
// email_n = ToLower(TrimSpace(email))
// salt = SHA-256("1f916-vault-v1|" + email_n)
// seed = argon2.IDKey(password, salt, t=3, m=262144, p=1, keyLen=32)
// locator = hex(HKDF-SHA256(secret=seed, salt=nil, info="locator", L=32))[:32]
// kek = HKDF-SHA256(secret=seed, salt=nil, info="kek", L=32)
func DeriveKeys(email, password string) (*KeyDerivation, error) {
	derivationMutex.Lock()
	defer derivationMutex.Unlock()

	emailNorm := strings.ToLower(strings.TrimSpace(email))
	saltInput := "1f916-vault-v1|" + emailNorm
	salt := sha256.Sum256([]byte(saltInput))

	// Password used EXACTLY as typed (no trimming, no case folding, no Unicode normalization)
	seed := argon2.IDKey([]byte(password), salt[:], 3, 262144, 1, 32)

	// HKDF locator with nil salt, info="locator", L=32
	locBytes, err := hkdf.Key(sha256.New, seed, nil, "locator", 32)
	if err != nil {
		ZeroBytes(seed)
		return nil, fmt.Errorf("failed to derive locator: %w", err)
	}
	locator := hex.EncodeToString(locBytes)[:32] // [:32] slices 32 hex chars (128 bits)

	// HKDF KEK with nil salt, info="kek", L=32
	kek, err := hkdf.Key(sha256.New, seed, nil, "kek", 32)
	if err != nil {
		ZeroBytes(seed)
		ZeroBytes(locBytes)
		return nil, fmt.Errorf("failed to derive KEK: %w", err)
	}

	return &KeyDerivation{
		Seed:    seed,
		Locator: locator,
		KEK:     kek,
	}, nil
}
