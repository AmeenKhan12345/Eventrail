package auth

import (
	"testing"
)

func TestGenerateParseHash(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		plaintext, prefix, hash, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if seen[plaintext] {
			t.Fatalf("duplicate key %q", plaintext)
		}
		seen[plaintext] = true

		gotPrefix, err := Parse(plaintext)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if gotPrefix != prefix {
			t.Fatalf("prefix = %q, want %q", gotPrefix, prefix)
		}
		if hash != Hash(plaintext) || Hash(plaintext) != Hash(plaintext) {
			t.Fatalf("hash is not the deterministic SHA-256 of the plaintext")
		}
		if Hash(plaintext) == Hash(plaintext+"x") {
			t.Fatal("different keys hashed equal")
		}
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	valid, _, _, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	tests := []string{
		"",
		"whk_live_short",
		"whk_test_" + valid[len("whk_live_"):],
		valid[:len(valid)-1],
		valid + "aa",
		"whk_live_AABBCCDD_" + valid[len("whk_live_aaaaaaaa_"):],
		valid[:len("whk_live_")+8] + "-" + valid[len("whk_live_")+9:],
		"Bearer " + valid,
	}
	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			if _, err := Parse(key); err == nil {
				t.Fatal("expected error")
			}
		})
	}

	if _, err := Parse(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
}
