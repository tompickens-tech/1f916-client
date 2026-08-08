package vault

import (
	"crypto/rand"
	"io"
	"testing"
)

func TestVaultBlobRoundTrip(t *testing.T) {
	kek := make([]byte, 32)
	io.ReadFull(rand.Reader, kek)

	pt := &VaultPlaintext{
		V:      1,
		Secret: "1f916_sk_test1234567890abcdef1234567890abcdef",
		Handle: "test-user",
	}

	blob, err := EncryptVaultBlob(kek, pt)
	if err != nil {
		t.Fatalf("EncryptVaultBlob failed: %v", err)
	}

	if len(blob) != VaultBlobSize {
		t.Errorf("Vault blob size = %d, want %d bytes", len(blob), VaultBlobSize)
	}

	decrypted, err := DecryptVaultBlob(kek, blob)
	if err != nil {
		t.Fatalf("DecryptVaultBlob failed: %v", err)
	}

	if decrypted.Secret != pt.Secret {
		t.Errorf("Decrypted secret = %s, want %s", decrypted.Secret, pt.Secret)
	}
	if decrypted.Handle != pt.Handle {
		t.Errorf("Decrypted handle = %s, want %s", decrypted.Handle, pt.Handle)
	}

	// Test wrong KEK authentication failure
	badKek := make([]byte, 32)
	io.ReadFull(rand.Reader, badKek)

	_, err = DecryptVaultBlob(badKek, blob)
	if err == nil {
		t.Errorf("Expected decryption error with bad KEK, got nil")
	}
}
