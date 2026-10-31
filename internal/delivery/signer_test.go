package delivery

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	secret := testSecret(t, 7)
	other := testSecret(t, 9)
	id := "evt_1"
	body := []byte(`{"n":1}`)
	stamp := time.Unix(1_700_000_000, 0)
	signature, err := Sign(secret, id, stamp, body)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	header := strconv.FormatInt(stamp.Unix(), 10)
	tolerance := time.Minute

	flipped := flipSignature(t, signature)

	tests := []struct {
		name      string
		secret    string
		id        string
		timestamp string
		signature string
		body      []byte
		now       time.Time
		tolerance time.Duration
		wantErr   bool
	}{
		{name: "round trip", secret: secret, id: id, timestamp: header, signature: signature, body: body, now: stamp, tolerance: tolerance},
		{name: "within tolerance", secret: secret, id: id, timestamp: header, signature: signature, body: body, now: stamp.Add(tolerance), tolerance: tolerance},
		{name: "tampered body", secret: secret, id: id, timestamp: header, signature: signature, body: []byte(`{"n":2}`), now: stamp, tolerance: tolerance, wantErr: true},
		{name: "tampered id", secret: secret, id: "evt_2", timestamp: header, signature: signature, body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "tampered timestamp", secret: secret, id: id, timestamp: strconv.FormatInt(stamp.Add(time.Second).Unix(), 10), signature: signature, body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "tampered signature", secret: secret, id: id, timestamp: header, signature: flipped, body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "stale timestamp", secret: secret, id: id, timestamp: header, signature: signature, body: body, now: stamp.Add(tolerance + time.Second), tolerance: tolerance, wantErr: true},
		{name: "future timestamp", secret: secret, id: id, timestamp: header, signature: signature, body: body, now: stamp.Add(-tolerance - time.Second), tolerance: tolerance, wantErr: true},
		{name: "empty signature", secret: secret, id: id, timestamp: header, signature: "", body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "bad version", secret: secret, id: id, timestamp: header, signature: "v0," + strings.TrimPrefix(signature, "v1,"), body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "bad signature encoding", secret: secret, id: id, timestamp: header, signature: "v1,%%%%", body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "bad timestamp", secret: secret, id: id, timestamp: "soon", signature: signature, body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "empty timestamp", secret: secret, id: id, timestamp: "", signature: signature, body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "different secret", secret: other, id: id, timestamp: header, signature: signature, body: body, now: stamp, tolerance: tolerance, wantErr: true},
		{name: "malformed secret", secret: "not-a-secret", id: id, timestamp: header, signature: signature, body: body, now: stamp, tolerance: tolerance, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Verify(tt.secret, tt.id, tt.timestamp, tt.signature, tt.body, tt.now, tt.tolerance)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestSignRejectsMalformedSecret(t *testing.T) {
	tests := []struct {
		name   string
		secret string
	}{
		{name: "missing prefix", secret: base64.StdEncoding.EncodeToString([]byte("key"))},
		{name: "bad base64", secret: "whsec_%%%%"},
		{name: "empty", secret: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Sign(tt.secret, "evt_1", time.Unix(1, 0), []byte("x")); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func testSecret(t *testing.T, fill byte) string {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = fill
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(raw)
}

func flipSignature(t *testing.T, signature string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(signature, "v1,"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	raw[len(raw)-1] ^= 0xff
	return "v1," + base64.StdEncoding.EncodeToString(raw)
}
