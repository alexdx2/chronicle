package wiring

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type DetectionConfidence string

const (
	DetectionConfirmed DetectionConfidence = "confirmed" // executable / running app found
	DetectionProbable  DetectionConfidence = "probable"  // valid active config exists
	DetectionStale     DetectionConfidence = "stale"     // only an empty/old config dir
	DetectionAbsent    DetectionConfidence = "absent"
)

type VerificationLevel string

const (
	VerifyNone       VerificationLevel = "none"
	VerifyFile       VerificationLevel = "file"       // config written & marker present
	VerifyDiscovered VerificationLevel = "discovered" // host CLI lists our integration
	VerifyReachable  VerificationLevel = "reachable"  // our binary/server starts
	VerifyFunctional VerificationLevel = "functional" // end-to-end MCP call succeeded
)

type HealthState string

const (
	HealthNotInstalled    HealthState = "not-installed"
	HealthPlanned         HealthState = "planned"
	HealthInstalled       HealthState = "installed"
	HealthPendingApproval HealthState = "pending-approval"
	HealthHealthy         HealthState = "healthy"
	HealthDegraded        HealthState = "degraded"
	HealthPartial         HealthState = "partial"
	HealthFailed          HealthState = "failed"
)

type Artifact struct {
	Path      string `json:"path"`
	Ownership string `json:"ownership"` // "chronicle" | "user"
	Hash      string `json:"hash"`
}

type ConfigChange struct {
	Path   string `json:"path"`
	Key    string `json:"key"`
	Marker string `json:"marker"`
}

type AgentState struct {
	AdapterVersion int            `json:"adapterVersion"`
	InstalledAt    string         `json:"installedAt"`
	Detection      string         `json:"detection"`
	Installation   string         `json:"installation"`
	Verification   string         `json:"verification"`
	Health         string         `json:"health"`
	Artifacts      []Artifact     `json:"artifacts,omitempty"`
	ConfigChanges  []ConfigChange `json:"configChanges,omitempty"`
}

type State struct {
	SchemaVersion    int                   `json:"schemaVersion"`
	ChronicleVersion string                `json:"chronicleVersion"`
	Agents           map[string]AgentState `json:"agents"`
}

func LoadState() (*State, error) {
	data, err := os.ReadFile(StatePath())
	if os.IsNotExist(err) {
		return &State{SchemaVersion: 1, Agents: map[string]AgentState{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, err
	}
	if st.Agents == nil {
		st.Agents = map[string]AgentState{}
	}
	return &st, nil
}

func SaveState(st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(StatePath(), append(data, '\n'), 0644)
}

// TEMP until Task 2: non-atomic write so Task 1 stands alone.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}
