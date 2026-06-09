package store

import "encoding/json"

// LabConfig is the scan-lab block stored in graph_revisions.metadata under "lab".
// Zero value = normal interactive scan.
type LabConfig struct {
	Autopilot  bool              `json:"autopilot"`
	Answers    map[string]string `json:"answers,omitempty"`
	BaseDomain string            `json:"base_domain,omitempty"`
}

// LabConfigForRevision parses the lab block from revision metadata.
// Missing/empty metadata returns a zero config, not an error.
func (s *Store) LabConfigForRevision(revisionID int64) (LabConfig, error) {
	rev, err := s.GetRevision(revisionID)
	if err != nil {
		return LabConfig{}, err
	}
	var wrapper struct {
		Lab LabConfig `json:"lab"`
	}
	if rev.Metadata != "" {
		_ = json.Unmarshal([]byte(rev.Metadata), &wrapper) // malformed metadata → zero config
	}
	return wrapper.Lab, nil
}

// autoConfirmEntry is a single auto-answered checkpoint record.
// Field order (Checkpoint before Answer) matches the JSON key order expected by tests.
type autoConfirmEntry struct {
	Checkpoint string `json:"checkpoint"`
	Answer     string `json:"answer"`
}

// SetScanRunAutopilot marks a scan run as autopilot-driven.
func (s *Store) SetScanRunAutopilot(runID int64) error {
	_, err := s.db.Exec(`UPDATE scan_runs SET autopilot = 1 WHERE run_id = ?`, runID)
	return err
}

// AppendScanRunAutoConfirm records an auto-answered checkpoint on the run.
func (s *Store) AppendScanRunAutoConfirm(runID int64, checkpointID, answer string) error {
	var current string
	if err := s.db.QueryRow(`SELECT auto_confirms FROM scan_runs WHERE run_id = ?`, runID).Scan(&current); err != nil {
		return err
	}
	var list []autoConfirmEntry
	_ = json.Unmarshal([]byte(current), &list)
	list = append(list, autoConfirmEntry{Checkpoint: checkpointID, Answer: answer})
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE scan_runs SET auto_confirms = ? WHERE run_id = ?`, string(b), runID)
	return err
}
