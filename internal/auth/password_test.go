package auth

import (
	"testing"
)

func TestHashPassword(t *testing.T) {
	hash, err := HashPassword("changeme")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}
	if hash == "" {
		t.Fatal("HashPassword returned empty hash")
	}
	t.Logf("Generated hash: %s", hash)
}

func TestCheckPassword_Correct(t *testing.T) {
	hash, err := HashPassword("changeme")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	valid, err := CheckPassword("changeme", hash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if !valid {
		t.Fatal("Correct password was rejected")
	}
}

func TestCheckPassword_Wrong(t *testing.T) {
	hash, err := HashPassword("changeme")
	if err != nil {
		t.Fatalf("HashPassword error: %v", err)
	}

	valid, err := CheckPassword("wrongpassword", hash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if valid {
		t.Fatal("Wrong password was accepted")
	}
}

func TestCheckPassword_DBHash(t *testing.T) {
	// This is the actual hash stored in the database for "changeme"
	dbHash := "$argon2id$v=19$m=65536,t=1,p=4$mucMvOaS6lZ2LWNS1OEFKw$UYEWv8cvCOO6l2zGeqv3JPVe1nyy0x9GXBfYEuDM544"

	valid, err := CheckPassword("changeme", dbHash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if !valid {
		t.Fatal("DB hash rejected correct password 'changeme'")
	}

	// Also verify wrong password is rejected
	valid, err = CheckPassword("wrongpassword", dbHash)
	if err != nil {
		t.Fatalf("CheckPassword error: %v", err)
	}
	if valid {
		t.Fatal("DB hash accepted wrong password")
	}
}
