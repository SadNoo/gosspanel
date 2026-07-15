package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	passwordHashScheme = "pbkdf2-sha256"
	passwordIterations = 210000
	passwordSaltBytes  = 16
	passwordKeyBytes   = 32
)

func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password is required")
	}
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2SHA256([]byte(password), salt, passwordIterations, passwordKeyBytes)
	return fmt.Sprintf("%s$%d$%s$%s",
		passwordHashScheme,
		passwordIterations,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(password string, stored string) bool {
	iterations, salt, want, ok := parsePasswordHash(stored)
	if !ok {
		return subtle.ConstantTimeCompare([]byte(password), []byte(stored)) == 1
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func IsPasswordHash(stored string) bool {
	_, _, _, ok := parsePasswordHash(stored)
	return ok
}

func PasswordNeedsRehash(stored string) bool {
	iterations, _, _, ok := parsePasswordHash(stored)
	return !ok || iterations < passwordIterations
}

func parsePasswordHash(stored string) (int, []byte, []byte, bool) {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != passwordHashScheme {
		return 0, nil, nil, false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 {
		return 0, nil, nil, false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(salt) == 0 {
		return 0, nil, nil, false
	}
	want, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return 0, nil, nil, false
	}
	return iterations, salt, want, true
}

func pbkdf2SHA256(password []byte, salt []byte, iterations int, keyLen int) []byte {
	hashLen := sha256.Size
	blocks := (keyLen + hashLen - 1) / hashLen
	out := make([]byte, 0, blocks*hashLen)
	var blockIndex [4]byte

	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(blockIndex[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write(blockIndex[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)

		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
