package tools

import (
	"sync"
	"testing"
)

// TestRemoveTaskNoDeadlock stresses the RemoveTask -> killTaskInternal path
// while the task is running, to prove the tm.mu -> task.mu lock order
// does not deadlock.
func TestRemoveTaskNoDeadlock(t *testing.T) {
	tm := NewBackgroundTaskManager()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		task, err := tm.StartTask("sleep 0.5", "", nil)
		if err != nil {
			t.Fatalf("StartTask: %v", err)
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := tm.RemoveTask(id); err != nil {
				t.Errorf("RemoveTask(%s): %v", id, err)
			}
		}(task.ID)
	}
	wg.Wait()
	if got := len(tm.ListTasks()); got != 0 {
		t.Fatalf("expected all tasks removed, got %d remaining", got)
	}
}
