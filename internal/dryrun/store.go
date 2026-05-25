package dryrun

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Entry struct {
	ID            string
	Host          string
	Database      string
	Query         string
	Kind          string
	Tables        []string
	ExplainResult string
	EstimatedRows int64
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

type Store struct {
	mu      sync.Mutex
	entries map[string]*Entry
	timeout time.Duration
}

func NewStore(timeout time.Duration) *Store {
	return &Store{
		entries: make(map[string]*Entry),
		timeout: timeout,
	}
}

func (s *Store) Add(host, database, query, kind string, tables []string, explainResult string, estimatedRows int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanupLocked()

	id := uuid.New().String()
	now := time.Now()
	s.entries[id] = &Entry{
		ID:            id,
		Host:          host,
		Database:      database,
		Query:         query,
		Kind:          kind,
		Tables:        tables,
		ExplainResult: explainResult,
		EstimatedRows: estimatedRows,
		CreatedAt:     now,
		ExpiresAt:     now.Add(s.timeout),
	}
	return id
}

func (s *Store) Get(id string) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[id]
	if !ok {
		return nil, fmt.Errorf("dry-run %s not found", id)
	}
	if time.Now().After(entry.ExpiresAt) {
		delete(s.entries, id)
		return nil, fmt.Errorf("dry-run %s has expired", id)
	}
	return entry, nil
}

func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, id)
}

func (s *Store) cleanupLocked() {
	now := time.Now()
	for id, entry := range s.entries {
		if now.After(entry.ExpiresAt) {
			delete(s.entries, id)
		}
	}
}
