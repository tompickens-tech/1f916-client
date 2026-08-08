package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

type DecoyBlob struct {
	Locator   string
	SizeClass int // 512 or 4096
	Data      []byte
}

func GenerateDecoys() ([]DecoyBlob, error) {
	decoys := make([]DecoyBlob, 0, 16)

	for i := 0; i < 16; i++ {
		locBytes := make([]byte, 16)
		if _, err := io.ReadFull(rand.Reader, locBytes); err != nil {
			return nil, fmt.Errorf("failed to generate decoy locator: %w", err)
		}
		locator := hex.EncodeToString(locBytes)[:32]

		targetSize := VaultBlobSize
		if i%2 == 1 {
			targetSize = DraftBlobSize
		}

		data, err := generateDecoyBlobData(targetSize)
		if err != nil {
			return nil, err
		}

		decoys = append(decoys, DecoyBlob{
			Locator:   locator,
			SizeClass: targetSize,
			Data:      data,
		})
	}

	return decoys, nil
}

func generateDecoyBlobData(targetSize int) ([]byte, error) {
	iv := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	ctLen := 64
	if targetSize == DraftBlobSize {
		ctLen = 512
	}
	ct := make([]byte, ctLen)
	if _, err := io.ReadFull(rand.Reader, ct); err != nil {
		return nil, err
	}

	env := VaultEnvelope{
		V: 1,
		KDF: KDFMeta{
			Name: "argon2id",
			M:    262144,
			T:    3,
			P:    1,
		},
		IV:  base64.StdEncoding.EncodeToString(iv),
		CT:  base64.StdEncoding.EncodeToString(ct),
		Pad: "",
	}

	dummyBytes, _ := json.Marshal(env)
	neededPadLen := targetSize - len(dummyBytes)
	if neededPadLen > 0 {
		padBytes := make([]byte, neededPadLen)
		io.ReadFull(rand.Reader, padBytes)
		env.Pad = base64.RawURLEncoding.EncodeToString(padBytes)
	}

	finalBytes, _ := json.Marshal(env)
	if len(finalBytes) != targetSize {
		diff := targetSize - len(finalBytes)
		if diff > 0 {
			env.Pad += stringsRepeat("A", diff)
		} else if diff < 0 && len(env.Pad) >= -diff {
			env.Pad = env.Pad[:len(env.Pad)+diff]
		}
		finalBytes, _ = json.Marshal(env)
	}

	if len(finalBytes) != targetSize {
		return nil, fmt.Errorf("decoy blob size assertion failed: got %d, want %d", len(finalBytes), targetSize)
	}

	return finalBytes, nil
}
