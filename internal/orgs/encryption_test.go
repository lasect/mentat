package orgs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestConnectionCipherRoundTripAndBinding(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	cipher, err := NewConnectionCipher(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	organizationID := uuid.New()
	databaseID := uuid.New()
	aad := connectionAAD(organizationID, databaseID, 1)
	ciphertext, nonce, version, err := cipher.Encrypt("postgres://mentat:secret@db.example/mentat", aad)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := cipher.Decrypt(ciphertext, nonce, aad, version)
	if err != nil {
		t.Fatal(err)
	}
	if plaintext != "postgres://mentat:secret@db.example/mentat" {
		t.Fatalf("plaintext = %q", plaintext)
	}
	if _, err := cipher.Decrypt(ciphertext, nonce, connectionAAD(organizationID, uuid.New(), 1), version); err == nil {
		t.Fatal("ciphertext decrypted for a different database")
	}
	if _, err := cipher.Decrypt(ciphertext, nonce, aad, 2); err == nil {
		t.Fatal("ciphertext decrypted with a different key version")
	}
}

func TestOrganizationInputNormalization(t *testing.T) {
	slug, err := NormalizeSlug("  Mentat Cloud! ")
	if err != nil {
		t.Fatal(err)
	}
	if slug != "mentat-cloud" {
		t.Fatalf("slug = %q", slug)
	}
	if _, err := NormalizeSlug("---"); err == nil {
		t.Fatal("invalid slug succeeded")
	}
	connection, err := ValidateConnectionString(" postgres://user:secret@localhost:5432/app ")
	if err != nil {
		t.Fatal(err)
	}
	if connection != "postgres://user:secret@localhost:5432/app" {
		t.Fatalf("connection = %q", connection)
	}
	for _, invalid := range []string{"", "mysql://user:secret@localhost/app", "postgres://localhost", "postgres:///app"} {
		if _, err := ValidateConnectionString(invalid); err == nil {
			t.Fatalf("ValidateConnectionString(%q) succeeded", invalid)
		}
	}
	if _, err := ValidateConnectionString("postgres://user:secret@localhost/" + strings.Repeat("a", 4096)); err == nil {
		t.Fatal("overlong connection string succeeded")
	}
}

func TestNormalizeExtensions(t *testing.T) {
	extensions, err := NormalizeExtensions([]string{"pg_stat_statements", " PG_MENTAT ", "pg_stat_statements"})
	if err != nil {
		t.Fatal(err)
	}
	if len(extensions) != 2 || extensions[0] != "pg_mentat" || extensions[1] != "pg_stat_statements" {
		t.Fatalf("extensions = %#v", extensions)
	}
	if _, err := NormalizeExtensions([]string{"postgis"}); err == nil {
		t.Fatal("unsupported extension succeeded")
	}
}
