/*
 * Copyright 2025 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Arvintian/chat-agent/pkg/logger"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	defaultContextDir = ".chat-agent/context"
)

// PersistenceStore implements CheckPointStore with simple JSON file persistence
type PersistenceStore struct {
	sessionID string
	filePath  string
	mu        sync.RWMutex
}

// NewPersistenceStore creates a new persistence store for the given session ID
func NewPersistenceStore(sessionID string) (*PersistenceStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	contextDir := filepath.Join(homeDir, defaultContextDir)
	filePath := filepath.Join(contextDir, fmt.Sprintf("%s.json", sessionID))

	// Ensure directory exists
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create context directory: %w", err)
	}

	store := &PersistenceStore{
		sessionID: sessionID,
		filePath:  filePath,
	}

	logger.Info("store", fmt.Sprintf("created persistence store with file: %s", filePath))

	return store, nil
}

// loadData loads all data from the JSON file
func (s *PersistenceStore) loadData() (map[string]string, error) {
	data := make(map[string]string)

	file, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Debug("store", fmt.Sprintf("file not found: %s, returning empty data", s.filePath))
			return data, nil
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&data); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("failed to decode file: %w", err)
	}

	return data, nil
}

// saveData saves all data to the JSON file
func (s *PersistenceStore) saveData(data map[string]string) error {
	file, err := os.OpenFile(s.filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file for writing: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to encode data: %w", err)
	}

	if err := file.Sync(); err != nil {
		logger.Warn("store", fmt.Sprintf("failed to sync file: %v", err))
	}

	return nil
}

// Set persists the checkpoint data to file
func (s *PersistenceStore) Set(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load existing data
	data, err := s.loadData()
	if err != nil {
		return fmt.Errorf("failed to load existing data: %w", err)
	}

	// Update or add the key-value pair
	data[key] = string(value)

	// Save back to file
	if err := s.saveData(data); err != nil {
		return err
	}

	logger.Debug("store", fmt.Sprintf("saved key '%s' to file %s", key, s.filePath))
	return nil
}

// Get retrieves the checkpoint data from file
func (s *PersistenceStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := s.loadData()
	if err != nil {
		return nil, false, err
	}

	value, ok := data[key]
	if !ok {
		return nil, false, nil
	}

	return []byte(value), true, nil
}

// Clear removes all persisted data for this session
func (s *PersistenceStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove file: %w", err)
	}

	logger.Info("store", fmt.Sprintf("cleared file: %s", s.filePath))
	return nil
}

// Close releases resources (no-op for file-based storage)
func (s *PersistenceStore) Close() error {
	return nil
}

// LoadMessages loads all messages from the persistence store
func (s *PersistenceStore) LoadMessages() ([]*schema.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := s.loadData()
	if err != nil {
		return nil, err
	}

	value, ok := data["messages"]
	if !ok {
		logger.Debug("store", fmt.Sprintf("no messages key found in file %s, returning empty messages", s.filePath))
		return []*schema.Message{}, nil
	}

	logger.Debug("store", fmt.Sprintf("found messages key in file %s, value length: %d", s.filePath, len(value)))

	var messages []*schema.Message
	if err := json.Unmarshal([]byte(value), &messages); err != nil {
		logger.Warn("store", fmt.Sprintf("failed to unmarshal messages from file %s: %v, value: %s", s.filePath, err, value))
		return []*schema.Message{}, nil
	}

	logger.Info("store", fmt.Sprintf("successfully loaded %d messages from file %s", len(messages), s.filePath))
	return messages, nil
}

// SaveMessages saves messages to the persistence store
func (s *PersistenceStore) SaveMessages(messages []*schema.Message) error {
	data, err := json.Marshal(messages)
	if err != nil {
		return fmt.Errorf("failed to marshal messages: %w", err)
	}

	return s.Set(context.Background(), "messages", data)
}

// InMemoryCheckPointStore wraps a compose.CheckPointStore with additional functionality
type InMemoryCheckPointStore struct {
	base    compose.CheckPointStore
	session *PersistenceStore
}

// NewInMemoryCheckPointStore creates a new in-memory checkpoint store
func NewInMemoryCheckPointStore() compose.CheckPointStore {
	return &inMemoryStore{
		mem: map[string][]byte{},
	}
}

// NewHybridCheckPointStore creates a hybrid store that uses both memory and persistence
func NewHybridCheckPointStore(persistence *PersistenceStore) *InMemoryCheckPointStore {
	return &InMemoryCheckPointStore{
		base:    NewInMemoryStore(),
		session: persistence,
	}
}

// Set delegates to the base store and also persists
func (s *InMemoryCheckPointStore) Set(ctx context.Context, key string, value []byte) error {
	if err := s.base.Set(ctx, key, value); err != nil {
		return err
	}
	if s.session != nil {
		return s.session.Set(ctx, key, value)
	}
	return nil
}

// Get first checks memory, then falls back to persistence
func (s *InMemoryCheckPointStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if value, ok, err := s.base.Get(ctx, key); err == nil && ok {
		return value, ok, nil
	}
	if s.session != nil {
		return s.session.Get(ctx, key)
	}
	return nil, false, nil
}
