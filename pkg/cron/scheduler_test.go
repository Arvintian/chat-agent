package cron

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
)

// newTestRunner creates a chat runner with a small queue for tests.
func newTestRunner(size int) *chatRunner {
	return &chatRunner{
		queue: make(chan config.CronTask, size),
		done:  make(chan struct{}),
	}
}

// Tasks must be executed serially, in the order they were enqueued.
func TestWorkerExecutesSeriallyInOrder(t *testing.T) {
	var (
		mu     sync.Mutex
		order  []string
		cur    int
		maxCur int
	)

	s := NewScheduler(nil)
	s.executeFn = func(chatName string, task config.CronTask) {
		if chatName != "chat1" {
			t.Errorf("unexpected chat name %q", chatName)
		}
		mu.Lock()
		cur++
		if cur > maxCur {
			maxCur = cur
		}
		order = append(order, task.Expr)
		mu.Unlock()

		time.Sleep(5 * time.Millisecond)

		mu.Lock()
		cur--
		mu.Unlock()
	}

	runner := newTestRunner(8)
	go s.worker("chat1", runner)

	for i := 0; i < 5; i++ {
		runner.queue <- config.CronTask{Expr: fmt.Sprintf("task-%d", i)}
	}
	close(runner.queue)
	<-runner.done

	mu.Lock()
	defer mu.Unlock()
	want := []string{"task-0", "task-1", "task-2", "task-3", "task-4"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("execution order = %v, want %v", order, want)
	}
	if maxCur != 1 {
		t.Fatalf("max concurrent executions = %d, want 1", maxCur)
	}
}

// A full queue drops new fires instead of blocking the cron loop.
func TestEnqueueDropsWhenQueueFull(t *testing.T) {
	s := NewScheduler(nil)
	runner := newTestRunner(2)
	s.runners["chat1"] = runner

	s.enqueue("chat1", config.CronTask{Expr: "a"})
	s.enqueue("chat1", config.CronTask{Expr: "b"})
	s.enqueue("chat1", config.CronTask{Expr: "c"}) // queue full -> dropped

	if got := len(runner.queue); got != 2 {
		t.Fatalf("queue length = %d, want 2", got)
	}
}

// Start with invalid/empty expressions must not panic, and Stop must return
// promptly even without any real firing.
func TestStartStopWithInvalidExpressions(t *testing.T) {
	cfg := &config.Config{
		Chats: map[string]config.Chat{
			"bad": {
				Cron: []config.CronTask{
					{Expr: "not-a-cron-expression", Prompt: "p"},
					{Expr: "", Prompt: "p"},
				},
			},
			"good": {
				Cron: []config.CronTask{
					{Expr: "0 0 * * *", Prompt: "p"},
				},
			},
			"none": {Cron: nil},
		},
	}
	s := NewScheduler(cfg)
	s.executeFn = func(string, config.CronTask) {}

	s.Start()

	if len(s.runners) != 2 { // "bad" and "good" both get a runner; "none" does not
		t.Fatalf("runners = %d, want 2", len(s.runners))
	}

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return in time")
	}
}

// Unattended cron runs must never wait for interactive approval.
func TestLogHandlerRejectsApproval(t *testing.T) {
	h := NewLogHandler("chat1", "0 0 * * *")
	_, err := h.SendApprovalRequest([]chatbot.ApprovalTarget{
		{ID: "id1", ToolName: "shell"},
		{ID: "id2", ToolName: "write_file"},
	})
	if err == nil {
		t.Fatal("expected approval to be rejected in cron mode")
	}
	if !strings.Contains(err.Error(), "shell") || !strings.Contains(err.Error(), "write_file") {
		t.Fatalf("error should list tool names: %v", err)
	}
}

// LogHandler must not panic on the full event sequence: response chunks are
// accumulated, thinking chunks and streaming tool-call updates are ignored.
func TestLogHandlerAccumulatesResponse(t *testing.T) {
	h := NewLogHandler("chat1", "0 0 * * *")
	h.SendChunk("part1 ", true, false, "response")
	h.SendChunk("part2", false, false, "thinking") // ignored
	h.SendChunk("", false, true, "response")       // end marker, no content
	h.SendComplete("")
	h.SendToolCall("shell", `{"cmd":"ls"}`, "id1", true)  // streaming update ignored
	h.SendToolCall("shell", `{"cmd":"ls"}`, "id1", false) // logged
	h.SendMessageCount()
	h.SendThinking(true)
	h.SendError("boom")
}
