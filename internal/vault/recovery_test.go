package vault

import (
	"encoding/base32"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoveryFileRoundTrip(t *testing.T) {
	email := "user@example.com"
	password := "SecretPass123!"
	pt := &VaultPlaintext{
		V:      1,
		Secret: "1f916_sk_recoverytestkey123456",
		Handle: "rec-user",
	}

	codeStr, codeBytes, err := GenerateRecoveryCode()
	if err != nil {
		t.Fatalf("GenerateRecoveryCode failed: %v", err)
	}

	rf, err := BuildRecoveryFile(email, password, pt, codeBytes)
	if err != nil {
		t.Fatalf("BuildRecoveryFile failed: %v", err)
	}

	rfJSON, err := json.Marshal(rf)
	if err != nil {
		t.Fatalf("Failed to marshal recovery file: %v", err)
	}
	rfStr := string(rfJSON)

	// Assert secrets do not appear in raw file
	if strings.Contains(rfStr, password) {
		t.Errorf("Password found in raw recovery file JSON!")
	}
	if strings.Contains(rfStr, codeStr) {
		t.Errorf("Recovery code string found in raw recovery file JSON!")
	}

	// 1. Test Password Door
	kd, err := DeriveKeys(email, password)
	if err != nil {
		t.Fatalf("DeriveKeys failed: %v", err)
	}
	defer kd.Zero()

	ptPasswordDoor, err := DecryptDoor(kd.KEK, rf.Vault)
	if err != nil {
		t.Fatalf("Password door failed: %v", err)
	}
	if ptPasswordDoor.Secret != pt.Secret {
		t.Errorf("Password door decrypted secret = %s, want %s", ptPasswordDoor.Secret, pt.Secret)
	}

	// 2. Test Escrow Door
	rawCodeBytes, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(codeStr)
	if err != nil {
		t.Fatalf("Failed to decode recovery code string: %v", err)
	}
	escrowKey, err := DeriveEscrowKey(rawCodeBytes)
	if err != nil {
		t.Fatalf("DeriveEscrowKey failed: %v", err)
	}
	defer ZeroBytes(escrowKey)

	ptEscrowDoor, err := DecryptDoor(escrowKey, rf.Escrow)
	if err != nil {
		t.Fatalf("Escrow door failed: %v", err)
	}
	if ptEscrowDoor.Secret != pt.Secret {
		t.Errorf("Escrow door decrypted secret = %s, want %s", ptEscrowDoor.Secret, pt.Secret)
	}
}
