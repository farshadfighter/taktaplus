package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

// TOTP per RFC 6238: SHA-1, 6 digits, 30 second steps, ±1 step tolerance.
const (
	totpDigits = 6
	totpStep   = 30
	totpSkew   = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret.
func NewTOTPSecret() []byte {
	s := make([]byte, 20)
	if _, err := rand.Read(s); err != nil {
		panic(err)
	}
	return s
}

// TOTPURI builds the otpauth:// URI that authenticator apps scan.
func TOTPURI(issuer, account string, secret []byte) string {
	v := url.Values{}
	v.Set("secret", b32.EncodeToString(secret))
	v.Set("issuer", issuer)
	v.Set("digits", fmt.Sprint(totpDigits))
	v.Set("period", fmt.Sprint(totpStep))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

// TOTPSecretText is the secret in the base32 form users can type in.
func TOTPSecretText(secret []byte) string { return b32.EncodeToString(secret) }

func totpAt(secret []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, secret)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, code%1_000_000)
}

// TOTPCode returns the code for t (used by tests).
func TOTPCode(secret []byte, t time.Time) string {
	return totpAt(secret, uint64(t.Unix()/totpStep))
}

// VerifyTOTP checks code against the steps around now.
func VerifyTOTP(secret []byte, code string, now time.Time) bool {
	if len(code) != totpDigits {
		return false
	}
	counter := now.Unix() / totpStep
	ok := 0
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		ok |= subtle.ConstantTimeCompare([]byte(totpAt(secret, uint64(counter+d))), []byte(code))
	}
	return ok == 1
}
