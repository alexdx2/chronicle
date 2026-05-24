package graph

import (
	"sync"
	"testing"
)

type testEmitter struct {
	mu     sync.Mutex
	events []ScanEvent
}

func (e *testEmitter) Emit(event ScanEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
}

func (e *testEmitter) Events() []ScanEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]ScanEvent{}, e.events...)
}

func TestNoopEmitter_DoesNotPanic(t *testing.T) {
	var e noopEmitter
	e.Emit(ScanEvent{Kind: EventPhaseChanged, Phase: "test"})
}

func TestSetEventEmitter_Nil(t *testing.T) {
	g := &Graph{emitter: noopEmitter{}}
	g.SetEventEmitter(nil)
	g.emitter.Emit(ScanEvent{Kind: EventScanComplete})
}

func TestSetEventEmitter_Receives(t *testing.T) {
	te := &testEmitter{}
	g := &Graph{emitter: noopEmitter{}}
	g.SetEventEmitter(te)

	g.emitter.Emit(ScanEvent{Kind: EventPhaseChanged, Phase: "test", Data: map[string]any{"msg": "hello"}})

	events := te.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Kind != EventPhaseChanged {
		t.Errorf("kind = %q, want %q", events[0].Kind, EventPhaseChanged)
	}
	if events[0].Phase != "test" {
		t.Errorf("phase = %q, want 'test'", events[0].Phase)
	}
}
