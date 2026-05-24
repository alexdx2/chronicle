package graph

// ScanEventKind identifies the type of scan event.
type ScanEventKind string

const (
	EventPhaseChanged    ScanEventKind = "phase_changed"
	EventBatchExtracted  ScanEventKind = "batch_extracted"
	EventResolveComplete ScanEventKind = "resolve_complete"
	EventFlowTraced      ScanEventKind = "flow_traced"
	EventScanComplete    ScanEventKind = "scan_complete"
)

// ScanEvent is a semantic event emitted during scanning.
// Consumed by the admin dashboard via WebSocket.
type ScanEvent struct {
	Kind     ScanEventKind  `json:"kind"`
	Phase    string         `json:"phase"`
	Data     map[string]any `json:"data"`
	Progress *ScanProgress  `json:"progress,omitempty"`
}

// EventEmitter receives scan events. Nil-safe — callers don't need to check.
type EventEmitter interface {
	Emit(event ScanEvent)
}

// noopEmitter is the default when no listener is attached.
type noopEmitter struct{}

func (noopEmitter) Emit(ScanEvent) {}
