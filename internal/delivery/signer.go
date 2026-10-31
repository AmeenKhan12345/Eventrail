package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

const secretPrefix = "whsec_"

var (
	errMalformedSecret    = errors.New("secret is malformed")
	errMalformedTimestamp = errors.New("timestamp is invalid")
	errTimestampSkew      = errors.New("timestamp is outside the tolerance")
	errMalformedSignature = errors.New("signature is malformed")
	errBadSignature       = errors.New("signature does not match")
)

// Sign builds the webhook-signature value for one delivery attempt.
// The signed text is id + "." + unix seconds + "." + body.
// The result is "v1," plus standard base64 of the HMAC-SHA256.
func Sign(secret, id string, timestamp time.Time, body []byte) (string, error) {
	mac, err := signatureMAC(secret, id, strconv.FormatInt(timestamp.Unix(), 10), body)
	if err != nil {
		return "", err
	}
	return "v1," + base64.StdEncoding.EncodeToString(mac), nil
}

// Verify checks a delivery signature. timestampHeader is the webhook-timestamp
// value and signatureHeader is the webhook-signature value. A clock skew
// larger than tolerance, or any malformed input, returns an error.
func Verify(secret, id, timestampHeader, signatureHeader string, body []byte, now time.Time, tolerance time.Duration) error {
	seconds, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return errMalformedTimestamp
	}
	when := time.Unix(seconds, 0)
	if now.Sub(when).Abs() > tolerance {
		return errTimestampSkew
	}
	expected, err := signatureMAC(secret, id, strconv.FormatInt(seconds, 10), body)
	if err != nil {
		return err
	}
	provided, err := decodeSignature(signatureHeader)
	if err != nil {
		return err
	}
	if !hmac.Equal(expected, provided) {
		return errBadSignature
	}
	return nil
}

func signatureMAC(secret, id, unixSeconds string, body []byte) ([]byte, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte{'.'})
	_, _ = mac.Write([]byte(unixSeconds))
	_, _ = mac.Write([]byte{'.'})
	_, _ = mac.Write(body)
	return mac.Sum(nil), nil
}

func decodeSecret(secret string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret, secretPrefix)
	if !ok || encoded == "" {
		return nil, errMalformedSecret
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) == 0 {
		return nil, errMalformedSecret
	}
	return key, nil
}

func decodeSignature(header string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(header, "v1,")
	if !ok || encoded == "" {
		return nil, errMalformedSignature
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		return nil, errMalformedSignature
	}
	return raw, nil
}
