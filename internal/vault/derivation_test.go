package vault

import (
	"testing"
)

func TestGoldenVectorDerivation(t *testing.T) {
	email := "user@example.com"
	password := "SecretPass123!"

	kd, err := DeriveKeys(email, password)
	if err != nil {
		t.Fatalf("DeriveKeys failed: %v", err)
	}
	defer kd.Zero()

	if len(kd.Locator) != 32 {
		t.Errorf("Locator length = %d, want 32 hex chars", len(kd.Locator))
	}
	if len(kd.KEK) != 32 {
		t.Errorf("KEK length = %d, want 32 bytes", len(kd.KEK))
	}
	if len(kd.Seed) != 32 {
		t.Errorf("Seed length = %d, want 32 bytes", len(kd.Seed))
	}

	// Golden vector matching Argon2id (m=262144, t=3, p=1) + HKDF-SHA256
	expectedLocator := "36de649f7f77ca5a9fe712c0162e6af5"
	if kd.Locator != expectedLocator {
		t.Errorf("Golden vector mismatch for locator: got %s, want %s", kd.Locator, expectedLocator)
	}

	// Verify exact password rule: " SecretPass123! " must produce a DIFFERENT locator
	kdSpaces, err := DeriveKeys(email, " SecretPass123! ")
	if err != nil {
		t.Fatalf("DeriveKeys with spaces failed: %v", err)
	}
	defer kdSpaces.Zero()

	if kdSpaces.Locator == kd.Locator {
		t.Errorf("Password with spaces produced same locator as password without spaces (violates exact password rule)")
	}
}
