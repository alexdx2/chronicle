package wiring

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type JournalStep struct {
	Target     string      `json:"target"`
	Action     string      `json:"action"`
	Existed    bool        `json:"existed"`
	Mode       os.FileMode `json:"mode,omitempty"`
	BackupPath string      `json:"backupPath,omitempty"`
	Applied    bool        `json:"applied"`
}

type ApplyJournal struct {
	OperationID string        `json:"operationId"`
	Agent       string        `json:"agent"`
	StartedAt   string        `json:"startedAt"`
	Status      string        `json:"status"` // in-progress|committed|rolled-back|partial
	Steps       []JournalStep `json:"steps"`
}

type ApplyResult struct {
	OperationID string
	Status      string
	Applied     int
}

func journalPath(opID string) string { return filepath.Join(Home(), "journal", opID+".json") }
func backupDir(opID string) string   { return filepath.Join(Home(), "backups", opID) }

func saveJournal(j *ApplyJournal) error {
	data, _ := json.MarshalIndent(j, "", "  ")
	return AtomicWrite(journalPath(j.OperationID), append(data, '\n'), 0644)
}

// ApplyPlan executes every mutating change with write-ahead journaling:
// backup existing target → journal step → atomic write/delete. On failure it
// rolls back in reverse order; an incomplete rollback yields status
// "partial" — the honest limit of per-file atomicity (see spec).
func ApplyPlan(p *Plan) (*ApplyResult, error) {
	opID := fmt.Sprintf("%s-%d", p.Agent, time.Now().UnixNano())
	j := &ApplyJournal{OperationID: opID, Agent: p.Agent,
		StartedAt: time.Now().UTC().Format(time.RFC3339), Status: "in-progress"}
	res := &ApplyResult{OperationID: opID}

	rollback := func(applyErr error) (*ApplyResult, error) {
		rollbackOK := true
		for i := len(j.Steps) - 1; i >= 0; i-- {
			s := j.Steps[i]
			if !s.Applied {
				continue
			}
			var rerr error
			switch {
			case s.Existed && s.BackupPath != "":
				var data []byte
				data, rerr = os.ReadFile(s.BackupPath)
				if rerr == nil {
					mode := s.Mode
					if mode == 0 {
						mode = 0644
					}
					rerr = AtomicWrite(s.Target, data, mode)
				}
			case !s.Existed:
				rerr = os.Remove(s.Target)
				if rerr != nil && os.IsNotExist(rerr) {
					rerr = nil // already gone — rollback goal achieved
				}
			}
			if rerr != nil {
				rollbackOK = false
			}
		}
		if rollbackOK {
			j.Status = "rolled-back"
			os.RemoveAll(backupDir(opID))
		} else {
			j.Status = "partial" // backups kept for doctor recovery
		}
		res.Status = j.Status
		if saveErr := saveJournal(j); saveErr != nil {
			return res, fmt.Errorf("apply failed (%s, journal %s) [journal save also failed: %v]: %w", j.Status, journalPath(opID), saveErr, applyErr)
		}
		return res, fmt.Errorf("apply failed (%s, journal %s): %w", j.Status, journalPath(opID), applyErr)
	}

	for i, c := range p.Changes {
		if !c.Mutating() {
			continue
		}
		step := JournalStep{Target: c.Target, Action: c.Action}
		if info, err := os.Stat(c.Target); err == nil {
			step.Existed = true
			step.Mode = info.Mode()
			step.BackupPath = filepath.Join(backupDir(opID), fmt.Sprintf("%d.bak", i))
			data, rerr := os.ReadFile(c.Target)
			if rerr != nil {
				return rollback(rerr)
			}
			if werr := AtomicWrite(step.BackupPath, data, 0600); werr != nil {
				return rollback(werr)
			}
		}
		j.Steps = append(j.Steps, step)
		if err := saveJournal(j); err != nil {
			return rollback(err)
		}
		var err error
		if c.Action == ActionDelete {
			err = os.Remove(c.Target)
		} else {
			mode := step.Mode
			if mode == 0 {
				mode = 0644
			}
			err = AtomicWrite(c.Target, c.NewContent, mode)
		}
		if err != nil {
			return rollback(err)
		}
		j.Steps[len(j.Steps)-1].Applied = true
		if err := saveJournal(j); err != nil {
			return rollback(err)
		}
		res.Applied++
	}

	j.Status = "committed"
	if err := saveJournal(j); err != nil {
		return rollback(err)
	}
	os.RemoveAll(backupDir(opID))
	res.Status = "committed"
	return res, nil
}
