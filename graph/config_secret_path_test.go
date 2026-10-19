package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

// A config value whose path holds a token (Slack hook, Telegram bot) never
// reaches the graph or the journal: the table keeps scheme://host[:port] only.
func TestConfigURL_PathNeverEntersGraphOrJournal(t *testing.T) {
	const hook = "https://hooks.slack.com/services/T0AAAA/B0BBBB/SECRETTOKENXYZ"
	if clean, host, ok := SanitizeConfigURL(hook); !ok || clean != "https://hooks.slack.com" || host != "hooks.slack.com" {
		t.Fatalf("SanitizeConfigURL = %q %q %v", clean, host, ok)
	}
	if clean, _, _ := SanitizeConfigURL("https://api.telegram.org/bot123:ABCsecret/sendMessage?x=1"); clean != "https://api.telegram.org" {
		t.Errorf("telegram = %q", clean)
	}
	cfg := &ConfigHosts{}
	cfg.Add("SLACK_WEBHOOK_URL", hook, "svc/.env")
	cfg.AddClient(ClientBinding{Class: "SlackClient", URL: hook, File: "svc/Program.cs"})
	cfg.AddUse(ConfigUse{File: "svc/src/alert.ts", Line: 3, Text: "process.env.SLACK_WEBHOOK_URL", Callee: "Slack", Symbol: "process.env.SLACK_WEBHOOK_URL"})
	g, s, _ := resolveFiles(t, cfg,
		srcFacts{"svc/src/alert.ts", []Fact{}},
		srcFacts{"svc/Services/SlackClient.cs", []Fact{
			{Kind: "http_call", From: "SlackClient", FromType: "provider", Target: "/send", Method: "POST"}}})
	if got := activeEdgesOf(t, s, "CALLS_SERVICE"); len(got) == 0 {
		t.Fatal("no CALLS_SERVICE edge")
	}
	_ = g
	n := 0
	_ = filepath.Walk(s.Dir(), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		b, _ := os.ReadFile(p)
		n++
		for _, secret := range []string{"SECRETTOKENXYZ", "T0AAAA", "B0BBBB", "hooks.slack.com/services"} {
			if strings.Contains(string(b), secret) {
				t.Errorf("%q found in %s: %s", secret, p, snippet(string(b), secret))
			}
		}
		return nil
	})
	if n == 0 {
		t.Fatal("no store files read")
	}
	edges, _ := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_SERVICE"})
	for _, e := range edges {
		evs, _ := s.ListEvidenceByEdge(e.EdgeID)
		for _, ev := range evs {
			if strings.Contains(ev.Assertion+ev.Metadata, "T0AAAA") {
				t.Errorf("evidence carries a config path: %+v", ev)
			}
		}
	}
}

func snippet(b, sub string) string {
	i := strings.Index(b, sub)
	return b[max(0, i-80):min(len(b), i+80)]
}
