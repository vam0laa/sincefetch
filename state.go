package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Snapshot is the little that has to be remembered to say what changed.
type Snapshot struct {
	Time          time.Time `json:"time"`
	BootID        string    `json:"boot_id"`
	Kernel        string    `json:"kernel"`
	Packages      int       `json:"packages"`
	Flatpaks      int       `json:"flatpaks"`
	Disks         []Disk    `json:"disks"`
	BatteryHealth float64   `json:"battery_health,omitempty"`
}

const (
	freshFor  = 12 * time.Hour      // a "last look" is at least this old, so the section is not empty in every new terminal
	minAge    = 10 * time.Minute    // before that there is nothing to compare with yet
	saveEvery = time.Hour           // at most one snapshot per hour
	keepFor   = 90 * 24 * time.Hour // older ones are dropped
)

func stateFile() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(dir, "sincefetch", "history.json")
}

func load(path string) ([]Snapshot, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h []Snapshot
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	return h, nil
}

// pick chooses what to compare with. want == 0 means "the last look".
// History is ordered oldest first.
func pick(h []Snapshot, now time.Time, want time.Duration) *Snapshot {
	if len(h) == 0 {
		return nil
	}
	limit := freshFor
	if want > 0 {
		limit = want
	}
	for i := len(h) - 1; i >= 0; i-- {
		if now.Sub(h[i].Time) >= limit {
			return &h[i]
		}
	}
	if now.Sub(h[0].Time) >= minAge {
		return &h[0]
	}
	return nil
}

func save(path string, h []Snapshot, s Snapshot) error {
	if n := len(h); n > 0 && s.Time.Sub(h[n-1].Time) < saveEvery {
		return nil
	}
	h = append(h, s)
	for len(h) > 1 && s.Time.Sub(h[0].Time) > keepFor {
		h = h[1:]
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	// write beside the file and rename, so two terminals opening at once cannot leave half a file
	tmp, err := os.CreateTemp(filepath.Dir(path), "history-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
