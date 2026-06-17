package backup

import "testing"

func TestEncodeFolderRoundTrip(t *testing.T) {
	cases := []string{
		"INBOX",
		"Clients/Acme",
		"Clients/AcmeCorp GmbH",
		"Sent Items",
		"Lists.dev.go-imap",
		"Ümläüte/Grüße",
		"weird%name",
		"a/b/c",
		"",
	}
	for _, name := range cases {
		enc := encodeFolder(name)
		if got := decodeFolder(enc); got != name {
			t.Errorf("round trip failed: %q -> %q -> %q", name, enc, got)
		}
		// The encoded form must be a single, filesystem-safe path segment.
		for i := 0; i < len(enc); i++ {
			c := enc[i]
			ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
				c == '.' || c == '_' || c == '-' || c == '%'
			if !ok {
				t.Errorf("encoded %q contains unsafe byte %q", enc, string(c))
			}
		}
	}
}

func TestEncodeFolderDistinct(t *testing.T) {
	// A literal slash and a percent-escaped one must not collide.
	if encodeFolder("a/b") == encodeFolder("a%2Fb") {
		t.Fatal("distinct folder names encoded to the same segment")
	}
	if decodeFolder(encodeFolder("a/b")) != "a/b" {
		t.Fatal("slash did not round-trip")
	}
}
