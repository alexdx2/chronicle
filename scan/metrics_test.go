package scan

import (
	"reflect"
	"testing"
	"time"

	"github.com/alexdx2/chronicle-core/headless"
)

func TestMetricsFrom(t *testing.T) {
	at := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	got := MetricsFrom("fake:fake", []headless.FileUsage{
		{Path: "a.ts", Status: "extracted", DurationMS: 7, Bytes: 120, Parts: 2, Facts: 3},
		{Path: "b.ts", Status: "failed", Error: "boom"},
	}, at)
	want := []FileMetric{
		{Path: "a.ts", Status: "extracted", Model: "fake:fake", DurationMS: 7, Bytes: 120, Parts: 2, Facts: 3, At: at},
		{Path: "b.ts", Status: "failed", Model: "fake:fake", At: at},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MetricsFrom = %+v, want %+v", got, want)
	}
	if empty := MetricsFrom("m", nil, at); empty == nil || len(empty) != 0 {
		t.Fatalf("MetricsFrom(nil) = %#v, want empty non-nil slice", empty)
	}
}
