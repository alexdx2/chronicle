package extract

import (
	"testing"
	"testing/fstest"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestScanConfigReviewedOnly(t *testing.T) {
	yml := `version: 1
channels:
  - id: reviewed-rule
    reviewed: true
    patterns: [{call: "bus.emit({name}", side: produce, transport: kafka}]
  - id: model-rule
    patterns: [{call: "bus.listen({name}", side: consume, transport: kafka}]
`
	fsys := fstest.MapFS{
		".depbot/scan.yaml": {Data: []byte(yml)},
		"svc/a.py":          {Data: []byte("TOPIC = 'ord.v1'\nbus.emit(TOPIC)\nbus.listen('x.y')\n")},
	}
	rep, err := Extract(fsys)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range rep.Facts {
		if f.Class == truth.ClassChannels {
			got[f.Key] = true
		}
	}
	if !got[truth.ChannelKey("topic", "ord.v1", "produce")] {
		t.Errorf("reviewed rule missing: %v", got)
	}
	if got[truth.ChannelKey("topic", "x.y", "consume")] {
		t.Errorf("unreviewed rule leaked into the reference: %v", got)
	}
}
