package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"regexp"
	"strings"
)

var loginPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)

func Login(s string) string    { return strings.ToLower(strings.TrimSpace(s)) }
func ValidLogin(s string) bool { return loginPattern.MatchString(s) }
func ValidRole(s string) bool  { return s == "admin" || s == "operator" || s == "viewer" }
func Password(s string) (string, error) {
	if len(s) < 12 || len(s) > 72 {
		return "", errors.New("пароль должен содержать от 12 до 72 байт")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(s), 12)
	return string(b), err
}
func Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func aead(key string) (cipher.AEAD, error) {
	b, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(b) != 32 {
		return nil, errors.New("APP_SECRET_KEY должен быть base64-ключом из 32 случайных байт")
	}
	c, err := aes.NewCipher(b)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(c)
}
func Encrypt(key, value string) (string, error) {
	c, err := aead(key)
	if err != nil {
		return "", err
	}
	n := make([]byte, c.NonceSize())
	if _, err = rand.Read(n); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(c.Seal(n, n, []byte(value), []byte("telegram-token-v1"))), nil
}
func Decrypt(key, value string) (string, error) {
	c, err := aead(key)
	if err != nil {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(b) < c.NonceSize() {
		return "", errors.New("повреждён секрет")
	}
	p, err := c.Open(nil, b[:c.NonceSize()], b[c.NonceSize():], []byte("telegram-token-v1"))
	return string(p), err
}
