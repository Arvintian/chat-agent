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
	"bufio"
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
// Each session uses two files:
// - .json file: stores checkpoint data (key-value pairs)
// - .jsonl file: stores messages in append-only mode (one JSON object per line)
type PersistenceStore struct {
	sessionID    string
	checkpointFile string // .json file for checkpoint
	messageFile  string   // .jsonl file for messages
	mu           sync.RWMutex
}

// NewPersistenceStore creates a new persistence store for the given session ID
func NewPersistenceStore(sessionID string) (*PersistenceStore, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	contextDir := filepath.Join(homeDir, defaultContextDir)
	checkpointFile := filepath.Join(contextDir, fmt.Sprintf("%s.json", sessionID))
	messageFile := filepath.Join(contextDir, fmt.Sprintf("%s.jsonl", sessionID))

	// Ensure directory exists
	if err := os.MkdirAll(contextDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create context directory: %w", err)
	}

	store := &PersistenceStore{
		sessionID:      sessionID,
		checkpointFile: checkpointFile,
		messageFile:    messageFile,
	}

	logger.Info("store", fmt.Sprintf("created persistence store with checkpoint file: %s, message file: %s", checkpointFile, messageFile))

	return store, nil
}

// loadData loads all checkpoint data from the JSON file
func (s *PersistenceStore) loadData() (map[string]string, error) {
	data := make(map[string]string)

	file, err := os.Open(s.checkpointFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Debug("store", fmt.Sprintf("checkpoint file not found: %s, returning empty data", s.checkpointFile))
			return data, nil
		}
		return nil, fmt.Errorf("failed to open checkpoint file: %w", err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&data); err != nil && err.Error() != "EOF" {
		return nil, fmt.Errorf("failed to decode checkpoint file: %w", err)
	}

	return data, nil
}

// saveData saves all checkpoint data to the JSON file
func (s *PersistenceStore) saveData(data map[string]string) error {
	file, err := os.OpenFile(s.checkpointFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint file for writing: %w", err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		return fmt.Errorf("failed to encode checkpoint data: %w", err)
	}

	if err := file.Sync(); err != nil {
		logger.Warn("store", fmt.Sprintf("failed to sync checkpoint file: %v", err))
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
		return fmt.Errorf("failed to load existing checkpoint data: %w", err)
	}

	// Update or add the key-value pair
	data[key] = string(value)

	// Save back to file
	if err := s.saveData(data); err != nil {
		return err
	}

	logger.Debug("store", fmt.Sprintf("saved checkpoint key '%s' to file %s", key, s.checkpointFile))
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

	// Remove checkpoint file
	if err := os.Remove(s.checkpointFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove checkpoint file: %w", err)
	}

	// Remove message file
	if err := os.Remove(s.messageFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove message file: %w", err)
	}

	logger.Info("store", fmt.Sprintf("cleared files for session %s: %s, %s", s.sessionID, s.checkpointFile, s.messageFile))
	return nil
}

// Close releases resources (no-op for file-based storage)
func (s *PersistenceStore) Close() error {
	return nil
}

// LoadMessages loads all messages from the JSONL file
// Each line in the JSONL file is a separate schema.Message object
func (s *PersistenceStore) LoadMessages() ([]*schema.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	file, err := os.Open(s.messageFile)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Debug("store", fmt.Sprintf("message file not found: %s, returning empty messages", s.messageFile))
			return []*schema.Message{}, nil
		}
		return nil, fmt.Errorf("failed to open message file: %w", err)
	}
	defer file.Close()

	var messages []*schema.Message
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if line == "" {
			continue // Skip empty lines
		}

		var msg schema.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			logger.Warn("store", fmt.Sprintf("failed to unmarshal message at line %d in file %s: %v", lineNum, s.messageFile, err))
			continue // Skip invalid lines
		}

		messages = append(messages, &msg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read message file: %w", err)
	}

	logger.Info("store", fmt.Sprintf("successfully loaded %d messages from file %s", len(messages), s.messageFile))
	return messages, nil
}

// SaveMessage appends a single message to the JSONL file in append-only mode
// Each message is written as a separate JSON object on its own line
// This method should be called with one message at a time for incremental storage
func (s *PersistenceStore) SaveMessage(msg *schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if msg == nil {
		return fmt.Errorf("message cannot be nil")
	}

	// Open file in append mode, create if not exists
	file, err := os.OpenFile(s.messageFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open message file for appending: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	// Write message as a single line
	if _, err := writer.WriteString(string(data) + "\n"); err != nil {
		return fmt.Errorf("failed to write message to file: %w", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush message file: %w", err)
	}

	if err := file.Sync(); err != nil {
		logger.Warn("store", fmt.Sprintf("failed to sync message file: %v", err))
	}

	logger.Debug("store", fmt.Sprintf("appended message to file %s", s.messageFile))
	return nil
}

// SaveMessagesOverwrite overwrites the entire JSONL file with the provided messages
// This is used when historical messages are modified (e.g., after compression)
// It completely rewrites the file instead of appending
func (s *PersistenceStore) SaveMessagesOverwrite(messages []*schema.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Open file in write mode, truncating existing content
	file, err := os.OpenFile(s.messageFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to open message file for writing: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)

	// Write all messages, one per line
	for _, msg := range messages {
		data, err := json.Marshal(msg)
		if err != nil {
			return fmt.Errorf("failed to marshal message: %w", err)
		}
		if _, err := writer.WriteString(string(data) + "\n"); err != nil {
			return fmt.Errorf("failed to write message to file: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush message file: %w", err)
	}

	if err := file.Sync(); err != nil {
		logger.Warn("store", fmt.Sprintf("failed to sync message file: %v", err))
	}

	logger.Info("store", fmt.Sprintf("rewrote %d messages to file %s", len(messages), s.messageFile))
	return nil
}

// SaveMessages appends multiple messages to the JSONL file
// For normal operation, prefer calling SaveMessage once per new message
func (s *PersistenceStore) SaveMessages(messages []*schema.Message) error {
	for _, msg := range messages {
		if err := s.SaveMessage(msg); err != nil {
			return err
		}
	}
	return nil
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
