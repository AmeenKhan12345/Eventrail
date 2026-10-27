package secrets

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestNewBox(t *testing.T) {
	tests := []struct {
		name    string
		key     []byte
		wantErr bool
	}{
		{name: "nil", key: nil, wantErr: true},
		{name: "empty", key: []byte{}, wantErr: true},
		{name: "16 bytes", key: bytes.Repeat([]byte{1}, 16), wantErr: true},
		{name: "31 bytes", key: bytes.Repeat([]byte{1}, 31), wantErr: true},
		{name: "33 bytes", key: bytes.Repeat([]byte{1}, 33), wantErr: true},
		{name: "32 bytes", key: bytes.Repeat([]byte{1}, 32), wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBox(tt.key)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("NewBox: %v", err)
			}
		})
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	box := testBox(t)
	tests := []string{"", "whsec_example", "signing secret 署名"}

	for _, plaintext := range tests {
		t.Run(plaintext, func(t *testing.T) {
			blob, err := box.Encrypt(plaintext)
			if err != nil {
				t.Fatalf("Encrypt: %v", err)
			}
			got, err := box.Decrypt(blob)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if got != plaintext {
				t.Fatalf("got %q, want %q", got, plaintext)
			}
		})
	}
}

func TestEncryptUsesAFreshNonce(t *testing.T) {
	box := testBox(t)
	first, err := box.Encrypt("same")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	second, err := box.Encrypt("same")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same plaintext were identical")
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	box := testBox(t)
	blob, err := box.Encrypt("same")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	flipped := bytes.Clone(blob)
	flipped[len(flipped)-1] ^= 0x01
	if _, err := box.Decrypt(flipped); err == nil {
		t.Fatal("Decrypt accepted a flipped byte")
	}

	unknown := bytes.Clone(blob)
	unknown[0] = 0x02
	if _, err := box.Decrypt(unknown); err == nil {
		t.Fatal("Decrypt accepted an unknown version")
	}

	other, err := NewBox(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	if _, err := other.Decrypt(blob); err == nil {
		t.Fatal("Decrypt accepted a blob sealed with a different key")
	}
}

func TestDecryptTruncated(t *testing.T) {
	box := testBox(t)
	blob, err := box.Encrypt("same")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	tests := [][]byte{
		nil,
		{},
		{0x01},
		blob[:10],
		blob[:len(blob)-1],
	}
	for _, tt := range tests {
		if _, err := box.Decrypt(tt); err == nil {
			t.Fatalf("Decrypt accepted %d bytes", len(tt))
		}
	}
}

func TestGenerateSecret(t *testing.T) {
	box := testBox(t)
	first, err := box.GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	second, err := box.GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	if len(first) < len("whsec_") || first[:len("whsec_")] != "whsec_" {
		t.Fatalf("secret %q missing prefix", first)
	}
	raw, err := base64.StdEncoding.DecodeString(first[len("whsec_"):])
	if err != nil {
		t.Fatalf("secret encoding: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("secret length = %d, want 32", len(raw))
	}
	if first == second {
		t.Fatal("two generated secrets were identical")
	}
}

func testBox(t *testing.T) *Box {
	t.Helper()
	box, err := NewBox(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}
