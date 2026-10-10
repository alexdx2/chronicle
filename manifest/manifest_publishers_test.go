package manifest

import "testing"

func TestManifest_PublishersParsed(t *testing.T) {
	m, err := Load([]byte(`
domains:
  d: {name: D}
publishers:
  allowed_signers: |
    chronicle-ci@okeep namespaces="git" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample
  revoked: |
    ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIRevoked
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Publishers == nil {
		t.Fatal("publishers not parsed")
	}
	if !m.HasPublishers() {
		t.Fatal("HasPublishers must be true with one signer line")
	}
	if m.Publishers.Revoked == "" {
		t.Fatal("revoked not parsed")
	}
}

func TestManifest_NoPublishers(t *testing.T) {
	m, err := Load([]byte("domains:\n  d: {name: D}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Publishers != nil || m.HasPublishers() {
		t.Fatal("absent section must be nil and HasPublishers false")
	}
}

func TestManifest_PublishersOnlyComments(t *testing.T) {
	m, err := Load([]byte("domains:\n  d: {name: D}\npublishers:\n  allowed_signers: |\n    # nothing yet\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.HasPublishers() {
		t.Fatal("comment-only allowed_signers must not count as configured publishers")
	}
}
