package wiring

type Ownership string

const (
	OwnershipChronicle Ownership = "chronicle"
	OwnershipUser      Ownership = "user"
)

const (
	ActionCreate    = "create"
	ActionUpdate    = "update"
	ActionUnchanged = "unchanged"
	ActionSkip      = "skip"
	ActionDelete    = "delete"
)

// PlannedChange is one file mutation, fully computed at Plan time.
type PlannedChange struct {
	Target     string
	Ownership  Ownership
	Action     string
	Summary    string
	NewContent []byte
}

type Plan struct {
	Agent   string
	Changes []PlannedChange
}

// Mutating reports whether the change writes or deletes anything.
func (c PlannedChange) Mutating() bool {
	return c.Action == ActionCreate || c.Action == ActionUpdate || c.Action == ActionDelete
}

type Detection struct {
	Confidence DetectionConfidence
	Evidence   []string
}

type Verification struct {
	Level  VerificationLevel
	Health HealthState
	Notes  []string
}

// Adapter wires one coding agent. Plan computes every change without
// writing; Apply is generic (ApplyPlan); Verify is tiered and honest —
// a config-only install must never report the same level as a functional
// MCP check.
type Adapter interface {
	ID() string
	DisplayName() string
	Detect() Detection
	Plan() (*Plan, error)
	Verify() Verification
	Remove() (*Plan, error)
}
