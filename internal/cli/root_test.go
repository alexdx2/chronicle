package cli

import "testing"

func TestJournalActorNonEmpty(t *testing.T) {
	actor := journalActor()
	if actor == "" {
		t.Fatal("journalActor() returned empty string; want git user.email, hostname, or \"unknown\"")
	}
	t.Logf("journalActor() = %q", actor)
}
