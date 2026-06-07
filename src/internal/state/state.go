package state

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"ddns/internal/provider"
)

type Store struct {
	Path string
}

type State struct {
	Records map[string]RecordState `json:"records"`
}

type RecordState struct {
	LastIP        string `json:"last_ip"`
	LastUpdatedAt string `json:"last_updated_at"`
}

func New(path string) *Store {
	return &Store{Path: path}
}

func (s *Store) Load() (State, error) {
	if s == nil || s.Path == "" {
		return emptyState(), nil
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return emptyState(), nil
	}
	if err != nil {
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, err
	}
	if st.Records == nil {
		st.Records = make(map[string]RecordState)
	}
	return st, nil
}

func (s *Store) Save(st State) error {
	if s == nil || s.Path == "" {
		return nil
	}
	if st.Records == nil {
		st.Records = make(map[string]RecordState)
	}
	if dir := filepath.Dir(s.Path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		_ = os.Remove(s.Path)
		if err := os.Rename(tmp, s.Path); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) MarkSuccess(target provider.TargetRecord, ip netip.Addr, at time.Time) error {
	st, err := s.Load()
	if err != nil {
		return err
	}
	st.Records[target.Key()] = RecordState{
		LastIP:        ip.String(),
		LastUpdatedAt: at.Format(time.RFC3339),
	}
	return s.Save(st)
}

func emptyState() State {
	return State{Records: make(map[string]RecordState)}
}
