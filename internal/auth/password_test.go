package auth

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("unexpected encoding: %q", encoded)
	}
	matched, err := VerifyPassword(encoded, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("valid password did not verify")
	}
	matched, err = VerifyPassword(encoded, "incorrect horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("invalid password verified")
	}
}

func TestPasswordValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "minimum", password: strings.Repeat("a", 12)},
		{name: "unicode minimum", password: strings.Repeat("界", 12)},
		{name: "maximum", password: strings.Repeat("a", 128)},
		{name: "too short", password: strings.Repeat("a", 11), wantErr: true},
		{name: "unicode too short", password: strings.Repeat("界", 11), wantErr: true},
		{name: "too long", password: strings.Repeat("a", 129), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidatePassword(test.password); (err != nil) != test.wantErr {
				t.Fatalf("ValidatePassword() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	for _, encoded := range []string{"", "argon2id", "$argon2id$v=19$m=nope,t=2,p=1$x$y", dummyPasswordHash + "$extra"} {
		if matched, err := VerifyPassword(encoded, "anything at all"); err == nil || matched {
			t.Fatalf("VerifyPassword(%q) = (%v, %v), want (false, error)", encoded, matched, err)
		}
	}
}

func TestVerifyPasswordRejectsExcessiveParameters(t *testing.T) {
	encoded := "$argon2id$v=19$m=1048576,t=2,p=1$c2FsdA$aGFzaA"
	if matched, err := VerifyPassword(encoded, "irrelevant password"); err == nil || matched {
		t.Fatalf("VerifyPassword() = (%v, %v), want (false, error)", matched, err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("  Person+Tetra@Example.COM ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "person+tetra@example.com" {
		t.Fatalf("NormalizeEmail() = %q", got)
	}
	for _, invalid := range []string{"", "not-an-email", "Name <name@example.com>", "a @example.com"} {
		if _, err := NormalizeEmail(invalid); err == nil {
			t.Fatalf("NormalizeEmail(%q) succeeded", invalid)
		}
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	got, err := NormalizeDisplayName("  Person Name  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Person Name" {
		t.Fatalf("NormalizeDisplayName() = %q", got)
	}
	if _, err := NormalizeDisplayName(strings.Repeat("界", 121)); err == nil {
		t.Fatal("overlong display name succeeded")
	}
}

func TestTruncatePreservesUTF8(t *testing.T) {
	got := truncate("ab界cd", 4)
	if got != "ab" || !utf8.ValidString(got) {
		t.Fatalf("truncate() = %q", got)
	}
}

func TestProviderAvatarURL(t *testing.T) {
	for _, value := range []string{"https://images.example/avatar.png", "http://localhost/avatar"} {
		if got := providerAvatarURL(value); got == nil || *got != value {
			t.Fatalf("providerAvatarURL(%q) = %#v", value, got)
		}
	}
	for _, value := range []string{"", "javascript:alert(1)", "/relative", strings.Repeat("a", 2049)} {
		if got := providerAvatarURL(value); got != nil {
			t.Fatalf("providerAvatarURL(%q) = %q", value, *got)
		}
	}
}
