// Package cron implements scheduled (cron) task execution for chats.
//
// Every chat preset may define a list of cron tasks (see config.CronTask).
// The Scheduler parses each task's 5-field cron expression and, on every
// tick, enqueues the task for that chat's worker; each worker executes its
// chat's tasks serially: the task hook runs first (its output must be JSON),
// the task prompt is rendered with the hook data and sent to the agent as
// the user message. Cron runs are always non-persistent.
package cron

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Arvintian/chat-agent/pkg/chatbot"
	"github.com/Arvintian/chat-agent/pkg/config"
	"github.com/Arvintian/chat-agent/pkg/logger"
	robfig "github.com/robfig/cron/v3"
)

// CronSessionID is the dedicated session ID used for cron executions so that
// cron runs are isolated from interactive sessions.
const CronSessionID = "cron"

// runTimeout bounds a single cron task execution.
const runTimeout = 30 * time.Minute

// stopWait bounds how long Stop waits for in-flight task executions to end.
const stopWait = 10 * time.Second

// queueSize bounds the pending-task queue of each chat; when full, new fires
// are dropped with a warning (tasks slower than their interval).
const queueSize = 32

// Scheduler runs cron tasks for all chats that define them.
type Scheduler struct {
	cfg *config.Config
	c   *robfig.Cron

	mu       sync.Mutex
	sessions map[string]*chatbot.ChatSession
	runners  map[string]*chatRunner

	jobCtx    context.Context
	jobCancel context.CancelFunc

	// executeFn runs a single dequeued task; it defaults to executeTask and
	// can be replaced in tests.
	executeFn func(chatName string, task config.CronTask)
}

// chatRunner serializes the cron task executions of one chat.
type chatRunner struct {
	queue chan config.CronTask
	done  chan struct{}
}

// NewScheduler creates a scheduler for the given configuration.
func NewScheduler(cfg *config.Config) *Scheduler {
	s := &Scheduler{
		cfg:      cfg,
		c:        robfig.New(),
		sessions: map[string]*chatbot.ChatSession{},
		runners:  map[string]*chatRunner{},
	}
	s.executeFn = s.executeTask
	return s
}

// Start registers every chat's cron tasks and starts the scheduler. Chats
// with invalid or empty expressions are skipped with a warning.
func (s *Scheduler) Start() {
	s.jobCtx, s.jobCancel = context.WithCancel(context.Background())

	for chatName, chat := range s.cfg.Chats {
		if len(chat.Cron) == 0 {
			continue
		}
		runner := &chatRunner{
			queue: make(chan config.CronTask, queueSize),
			done:  make(chan struct{}),
		}
		s.runners[chatName] = runner
		go s.worker(chatName, runner)

		for _, task := range chat.Cron {
			if task.Expr == "" {
				logger.Warn("cron", fmt.Sprintf("chat %s: cron task with empty expr skipped", chatName))
				continue
			}
			// Jobs only enqueue; the worker executes serially, so fires are
			// never dropped while a previous run is still in progress.
			if _, err := s.c.AddFunc(task.Expr, func() { s.enqueue(chatName, task) }); err != nil {
				logger.Warn("cron", fmt.Sprintf("chat %s: invalid cron expr %q: %v", chatName, task.Expr, err))
				continue
			}
			logger.Info("cron", fmt.Sprintf("chat %s: scheduled cron task %q", chatName, task.Expr))
		}
	}
	s.c.Start()
}

// Stop stops the scheduler: it aborts in-flight executions, waits (bounded)
// for the task workers to finish and closes all cron chat sessions.
func (s *Scheduler) Stop() {
	// Abort in-flight model calls so workers can drain promptly.
	s.jobCancel()

	// Wait for the cron loop to finish dispatching (jobs only enqueue, so
	// this returns immediately).
	stopCtx := s.c.Stop()
	<-stopCtx.Done()

	s.mu.Lock()
	for _, runner := range s.runners {
		close(runner.queue)
	}
	dones := make([]chan struct{}, 0, len(s.runners))
	for _, runner := range s.runners {
		dones = append(dones, runner.done)
	}
	sessions := s.sessions
	s.mu.Unlock()

	// Wait (bounded) for the workers to finish their current execution.
	var wg sync.WaitGroup
	for _, done := range dones {
		wg.Add(1)
		go func(d chan struct{}) {
			defer wg.Done()
			<-d
		}(done)
	}
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(stopWait):
		logger.Warn("cron", "timed out waiting for cron workers to stop")
	}

	for name, sess := range sessions {
		if err := sess.Close(); err != nil {
			logger.Warn("cron", fmt.Sprintf("chat %s: failed to close cron session: %v", name, err))
		}
	}
}

// enqueue hands a fired task to the chat's worker (serial execution).
func (s *Scheduler) enqueue(chatName string, task config.CronTask) {
	// runners is fully built in Start before the cron loop starts and is
	// never mutated afterwards, so it can be read lock-free here.
	runner := s.runners[chatName]
	select {
	case runner.queue <- task:
	default:
		logger.Warn("cron", fmt.Sprintf("chat %s: cron queue full, dropping task %q", chatName, task.Expr))
	}
}

// worker executes the queued tasks of one chat serially.
func (s *Scheduler) worker(chatName string, runner *chatRunner) {
	defer close(runner.done)
	for task := range runner.queue {
		s.executeFn(chatName, task)
	}
}

// executeTask runs a single cron task on the chat's dedicated session.
func (s *Scheduler) executeTask(chatName string, task config.CronTask) {
	sess, err := s.getOrCreateSession(chatName)
	if err != nil {
		logger.Error("cron", fmt.Sprintf("chat %s: failed to init cron session: %v", chatName, err))
		return
	}

	ctx, cancel := context.WithTimeout(s.jobCtx, runTimeout)
	defer cancel()

	handler := NewLogHandler(chatName, task.Expr)
	if err := sess.RunCronTask(ctx, task, handler); err != nil {
		if ctx.Err() != nil {
			logger.Warn("cron", fmt.Sprintf("chat %s: cron task %q aborted: %v", chatName, task.Expr, err))
		} else {
			logger.Error("cron", fmt.Sprintf("chat %s: cron task %q failed: %v", chatName, task.Expr, err))
		}
	}
}

// getOrCreateSession lazily creates (once per chat) the dedicated cron session.
// Cron runs are always non-persistent: the chat preset's persistence setting
// is ignored (a deep copy of the config with persistence disabled is used).
func (s *Scheduler) getOrCreateSession(chatName string) (*chatbot.ChatSession, error) {
	s.mu.Lock()
	if sess, ok := s.sessions[chatName]; ok {
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()

	cfg, err := s.cfg.DeepCopy()
	if err != nil {
		return nil, fmt.Errorf("failed to copy config: %w", err)
	}
	chat := cfg.Chats[chatName]
	chat.Persistence = false
	cfg.Chats[chatName] = chat

	sess, err := chatbot.InitChatSession(context.Background(), cfg, chatName, CronSessionID, false)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Only one worker per chat ever calls this, but stay safe anyway.
	if existing, ok := s.sessions[chatName]; ok {
		sess.Close()
		return existing, nil
	}
	s.sessions[chatName] = sess
	return sess, nil
}

// LogHandler is a chatbot.Handler that writes cron task output to the log
// instead of a client connection.
type LogHandler struct {
	chatName string
	expr     string

	mu       sync.Mutex
	response strings.Builder
}

// NewLogHandler creates a log handler for cron task output.
func NewLogHandler(chatName, expr string) *LogHandler {
	return &LogHandler{chatName: chatName, expr: expr}
}

func (h *LogHandler) SendChunk(content string, first, last bool, contentType string) {
	if content == "" {
		return
	}
	if contentType != "response" {
		return // thinking chunks are not logged
	}
	h.mu.Lock()
	h.response.WriteString(content)
	h.mu.Unlock()
}

func (h *LogHandler) SendToolCall(name string, arguments string, id string, streaming bool) {
	if streaming {
		return
	}
	logger.Info("cron", fmt.Sprintf("[%s/%s] tool call: %s", h.chatName, h.expr, name))
}

func (h *LogHandler) SendThinking(status bool) {}

func (h *LogHandler) SendComplete(message string) {
	h.mu.Lock()
	out := strings.TrimSpace(h.response.String())
	h.response.Reset()
	h.mu.Unlock()
	if out != "" {
		logger.Info("cron", fmt.Sprintf("[%s/%s] response: %s", h.chatName, h.expr, out))
	} else {
		logger.Info("cron", fmt.Sprintf("[%s/%s] completed", h.chatName, h.expr))
	}
}

func (h *LogHandler) SendError(err string) {
	logger.Error("cron", fmt.Sprintf("[%s/%s] error: %s", h.chatName, h.expr, err))
}

func (h *LogHandler) SendApprovalRequest(targets []chatbot.ApprovalTarget) (chatbot.ApprovalResultMap, error) {
	// Cron runs are unattended: tool approval can never be granted, so the
	// execution is aborted instead of waiting forever.
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.ToolName)
	}
	return nil, fmt.Errorf("tool approval is not available in cron mode: %s", strings.Join(names, ", "))
}

func (h *LogHandler) SendMessageCount() {}
