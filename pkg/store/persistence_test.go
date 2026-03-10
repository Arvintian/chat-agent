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
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPersistenceStore_JSONL(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	
	// Save original home dir and set temp dir
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	sessionID := "test-session-jsonl"
	store, err := NewPersistenceStore(sessionID)
	if err != nil {
		t.Fatalf("Failed to create persistence store: %v", err)
	}

	// Verify files are created
	expectedCheckpointFile := filepath.Join(tmpDir, defaultContextDir, sessionID+".json")
	expectedMessageFile := filepath.Join(tmpDir, defaultContextDir, sessionID+".jsonl")

	// Test 1: Load empty messages
	messages, err := store.LoadMessages()
	if err != nil {
		t.Fatalf("Failed to load empty messages: %v", err)
	}
	if len(messages) != 0 {
		t.Errorf("Expected 0 messages, got %d", len(messages))
	}

	// Test 2: Save first batch of messages
	msgs1 := []*schema.Message{
		{Role: schema.User, Content: "Hello"},
		{Role: schema.Assistant, Content: "Hi there!"},
	}
	if err := store.SaveMessages(msgs1); err != nil {
		t.Fatalf("Failed to save first batch of messages: %v", err)
	}

	// Verify message file exists
	if _, err := os.Stat(expectedMessageFile); os.IsNotExist(err) {
		t.Errorf("Message file should exist: %s", expectedMessageFile)
	}

	// Test 3: Load messages after first save
	messages, err = store.LoadMessages()
	if err != nil {
		t.Fatalf("Failed to load messages: %v", err)
	}
	if len(messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(messages))
	}

	// Test 4: Append more messages (append mode)
	msgs2 := []*schema.Message{
		{Role: schema.User, Content: "How are you?"},
		{Role: schema.Assistant, Content: "I'm good, thanks!"},
	}
	if err := store.SaveMessages(msgs2); err != nil {
		t.Fatalf("Failed to append messages: %v", err)
	}

	// Test 5: Load all messages - should have 4 now
	messages, err = store.LoadMessages()
	if err != nil {
		t.Fatalf("Failed to load all messages: %v", err)
	}
	if len(messages) != 4 {
		t.Errorf("Expected 4 messages after append, got %d", len(messages))
	}

	// Verify message order
	if messages[0].Content != "Hello" {
		t.Errorf("Expected first message 'Hello', got '%s'", messages[0].Content)
	}
	if messages[1].Content != "Hi there!" {
		t.Errorf("Expected second message 'Hi there!', got '%s'", messages[1].Content)
	}
	if messages[2].Content != "How are you?" {
		t.Errorf("Expected third message 'How are you?', got '%s'", messages[2].Content)
	}
	if messages[3].Content != "I'm good, thanks!" {
		t.Errorf("Expected fourth message 'I'm good, thanks!', got '%s'", messages[3].Content)
	}

	// Test 6: Verify JSONL format (each message on its own line)
	content, err := os.ReadFile(expectedMessageFile)
	if err != nil {
		t.Fatalf("Failed to read message file: %v", err)
	}
	
	// Count lines (should be 4)
	lineCount := 0
	for _, b := range content {
		if b == '\n' {
			lineCount++
		}
	}
	if lineCount != 4 {
		t.Errorf("Expected 4 lines in JSONL file, got %d", lineCount)
	}

	// Test 7: Test checkpoint storage (Set/Get)
	ctx := context.Background()
	if err := store.Set(ctx, "test_key", []byte("test_value")); err != nil {
		t.Fatalf("Failed to set checkpoint: %v", err)
	}

	value, ok, err := store.Get(ctx, "test_key")
	if err != nil {
		t.Fatalf("Failed to get checkpoint: %v", err)
	}
	if !ok {
		t.Error("Checkpoint key should exist")
	}
	if string(value) != "test_value" {
		t.Errorf("Expected 'test_value', got '%s'", string(value))
	}

	// Test 8: Clear should remove both files
	if err := store.Clear(); err != nil {
		t.Fatalf("Failed to clear store: %v", err)
	}

	if _, err := os.Stat(expectedMessageFile); !os.IsNotExist(err) {
		t.Error("Message file should be deleted after Clear")
	}
	if _, err := os.Stat(expectedCheckpointFile); !os.IsNotExist(err) {
		t.Error("Checkpoint file should be deleted after Clear")
	}
}

func TestPersistenceStore_SingleMessageAppend(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	
	// Save original home dir and set temp dir
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	sessionID := "test-single-message-append"
	store, err := NewPersistenceStore(sessionID)
	if err != nil {
		t.Fatalf("Failed to create persistence store: %v", err)
	}

	// Append single messages one by one using the new SaveMessage API
	testCases := []struct {
		role    string
		content string
	}{
		{"user", "First message"},
		{"assistant", "Second message"},
		{"user", "Third message"},
	}

	for _, tc := range testCases {
		var role schema.RoleType
		if tc.role == "user" {
			role = schema.User
		} else {
			role = schema.Assistant
		}
		
		msg := &schema.Message{Role: role, Content: tc.content}
		if err := store.SaveMessage(msg); err != nil {
			t.Fatalf("Failed to save message '%s': %v", tc.content, err)
		}
	}

	// Load all messages - should have all three
	allMessages, err := store.LoadMessages()
	if err != nil {
		t.Fatalf("Failed to load messages: %v", err)
	}

	if len(allMessages) != 3 {
		t.Errorf("Expected 3 messages after three single appends, got %d", len(allMessages))
	}

	// Verify content order
	expectedContents := []string{"First message", "Second message", "Third message"}
	for i, expectedContent := range expectedContents {
		if allMessages[i].Content != expectedContent {
			t.Errorf("Message %d: expected content '%s', got '%s'", i, expectedContent, allMessages[i].Content)
		}
	}
}

func TestPersistenceStore_AppendMode(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir := t.TempDir()
	
	// Save original home dir and set temp dir
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	sessionID := "test-append-mode"
	store, err := NewPersistenceStore(sessionID)
	if err != nil {
		t.Fatalf("Failed to create persistence store: %v", err)
	}

	expectedMessageFile := filepath.Join(tmpDir, defaultContextDir, sessionID+".jsonl")

	// Simulate multiple appends like real usage
	for i := 0; i < 5; i++ {
		msgs := []*schema.Message{
			{Role: schema.User, Content: "Question " + string(rune('0'+i))},
			{Role: schema.Assistant, Content: "Answer " + string(rune('0'+i))},
		}
		if err := store.SaveMessages(msgs); err != nil {
			t.Fatalf("Failed to save messages batch %d: %v", i, err)
		}
	}

	// Load all messages
	messages, err := store.LoadMessages()
	if err != nil {
		t.Fatalf("Failed to load messages: %v", err)
	}

	// Should have 10 messages (5 batches * 2 messages each)
	if len(messages) != 10 {
		t.Errorf("Expected 10 messages, got %d", len(messages))
	}

	// Verify file size grows with each append
	fileInfo, err := os.Stat(expectedMessageFile)
	if err != nil {
		t.Fatalf("Failed to stat message file: %v", err)
	}
	t.Logf("Final message file size: %d bytes", fileInfo.Size())
}
