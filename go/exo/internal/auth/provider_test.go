package auth

import (
	"encoding/base64"
	"testing"
)

func fakeJWT(payload string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return header + "." + body + ".sig"
}

func TestPersonIdentityFromToken_NameClaim(t *testing.T) {
	jwt := fakeJWT(`{"preferred_username":"alice","name":"Alice Smith","given_name":"Alice","family_name":"Smith"}`)
	unixID, label, ok := PersonIdentityFromToken(jwt)
	if !ok {
		t.Fatalf("PersonIdentityFromToken() ok = false, want true")
	}
	if unixID != "alice" {
		t.Errorf("unixID = %q, want %q", unixID, "alice")
	}
	if label != "Alice Smith" {
		t.Errorf("label = %q, want %q", label, "Alice Smith")
	}
}

func TestPersonIdentityFromToken_GivenFamilyNameFallback(t *testing.T) {
	jwt := fakeJWT(`{"preferred_username":"alice","given_name":"Alice","family_name":"Smith"}`)
	unixID, label, ok := PersonIdentityFromToken(jwt)
	if !ok {
		t.Fatalf("PersonIdentityFromToken() ok = false, want true")
	}
	if unixID != "alice" {
		t.Errorf("unixID = %q, want %q", unixID, "alice")
	}
	if label != "Alice Smith" {
		t.Errorf("label = %q, want %q", label, "Alice Smith")
	}
}

func TestPersonIdentityFromToken_NoNameClaims(t *testing.T) {
	jwt := fakeJWT(`{"preferred_username":"alice"}`)
	unixID, label, ok := PersonIdentityFromToken(jwt)
	if !ok {
		t.Fatalf("PersonIdentityFromToken() ok = false, want true")
	}
	if unixID != "alice" {
		t.Errorf("unixID = %q, want %q", unixID, "alice")
	}
	if label != "" {
		t.Errorf("label = %q, want empty", label)
	}
}

func TestPersonIdentityFromToken_MissingPreferredUsername(t *testing.T) {
	jwt := fakeJWT(`{"sub":"12345"}`)
	if _, _, ok := PersonIdentityFromToken(jwt); ok {
		t.Errorf("PersonIdentityFromToken() ok = true, want false")
	}
}

func TestPersonIdentityFromToken_MalformedToken(t *testing.T) {
	if _, _, ok := PersonIdentityFromToken("not-a-jwt"); ok {
		t.Errorf("PersonIdentityFromToken() ok = true, want false")
	}
}

func TestPersonIdentityFromToken_NoEmailField(t *testing.T) {
	jwt := fakeJWT(`{"preferred_username":"alice","name":"Alice Smith","email":"alice@example.com"}`)
	_, label, ok := PersonIdentityFromToken(jwt)
	if !ok {
		t.Fatalf("PersonIdentityFromToken() ok = false, want true")
	}
	if label != "Alice Smith" {
		t.Errorf("label = %q, want %q", label, "Alice Smith")
	}
}
