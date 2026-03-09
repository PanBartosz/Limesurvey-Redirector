package credentials

import "testing"

func TestProtectorRoundTrip(t *testing.T) {
	p, err := NewProtector("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewProtector failed: %v", err)
	}
	encrypted, err := p.Encrypt("rpc-password")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if encrypted == "rpc-password" || encrypted == "" {
		t.Fatalf("expected encrypted value, got %q", encrypted)
	}
	decrypted, err := p.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if decrypted != "rpc-password" {
		t.Fatalf("unexpected decrypted value %q", decrypted)
	}
}

func TestProtectorRejectsTamperedPayload(t *testing.T) {
	p, err := NewProtector("01234567890123456789012345678901")
	if err != nil {
		t.Fatalf("NewProtector failed: %v", err)
	}
	encrypted, err := p.Encrypt("rpc-password")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	_, err = p.Decrypt(encrypted[:len(encrypted)-1] + "A")
	if err == nil {
		t.Fatal("expected tampered payload to fail decryption")
	}
}
