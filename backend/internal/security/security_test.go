package security

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestPasswords(t *testing.T) {
	h, err := Password("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if h == "correct horse battery" || !Verify(h, "correct horse battery") || Verify(h, "wrong") {
		t.Fatal("password verification")
	}
	for _, v := range []string{"short", strings.Repeat("a", 73)} {
		if _, err := Password(v); err == nil {
			t.Fatal("accepted invalid password")
		}
	}
}
func TestSecrets(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cipher, err := Encrypt(key, "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	v, err := Decrypt(key, cipher)
	if err != nil || v != "secret-token" {
		t.Fatal("round trip", err)
	}
	wrong := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	if _, err = Decrypt(wrong, cipher); err == nil {
		t.Fatal("wrong key accepted")
	}
	raw, _ := base64.StdEncoding.DecodeString(cipher)
	raw[len(raw)-1] ^= 1
	if _, err = Decrypt(key, base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("tampering accepted")
	}
	if _, err = Encrypt("", "secret"); err == nil {
		t.Fatal("empty key accepted")
	}
	if len(Token()) != 43 || Token() == Token() {
		t.Fatal("token entropy/format")
	}
}
