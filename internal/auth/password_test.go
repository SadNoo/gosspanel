package auth

import "testing"

func TestPasswordHashVerify(t *testing.T) {
	hash, err := HashPassword("secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "secret-value" {
		t.Fatal("password was stored as plaintext")
	}
	if !IsPasswordHash(hash) {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword("secret-value", hash) {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword("wrong-value", hash) {
		t.Fatal("wrong password verified")
	}
}

func TestPasswordVerifyLegacyPlaintext(t *testing.T) {
	if !VerifyPassword("admin", "admin") {
		t.Fatal("legacy plaintext password should verify")
	}
	if !PasswordNeedsRehash("admin") {
		t.Fatal("legacy plaintext password should need rehash")
	}
}
