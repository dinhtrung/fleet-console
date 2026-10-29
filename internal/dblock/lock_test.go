package dblock_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dinhtrung/fleet-console/internal/dblock"
	"github.com/dinhtrung/fleet-console/internal/store"
)

func TestAcquireRejectsSecondInstance(t *testing.T) {
	db := filepath.Join(t.TempDir(), "fleet.db")
	first, err := dblock.Acquire(db)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, err = dblock.Acquire(db)
	if !errors.Is(err, dblock.ErrHeld) {
		t.Fatalf("second acquire = %v, want ErrHeld", err)
	}
	if err.Error() != "another instance owns "+db {
		t.Fatalf("error = %q, want it to name the database", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("second release: %v", err)
	}
	again, err := dblock.Acquire(db)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	defer again.Release()
}

// TestHelperDispatchHolder is the child side of the two-process test. It holds
// the lock and writes the only dispatch row, then sleeps until killed.
func TestHelperDispatchHolder(t *testing.T) {
	if os.Getenv("DBLOCK_HELPER") != "1" {
		return
	}
	db := os.Getenv("DBLOCK_DB")
	project, err := strconv.ParseInt(os.Getenv("DBLOCK_PROJECT"), 10, 64)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper: project:", err)
		os.Exit(1)
	}
	held, err := dblock.Acquire(db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper: acquire:", err)
		os.Exit(1)
	}
	_ = held
	st, err := store.Open(context.Background(), db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper: store:", err)
		os.Exit(1)
	}
	if _, err := st.CreateDispatch(context.Background(), store.NewDispatch{
		ProjectID: project, ItemNumber: 7, Attempt: 1, Seat: "builder", Branch: "feature/7",
		Worktree: "/tmp/wt", BaseSHA: "base",
	}); err != nil {
		fmt.Fprintln(os.Stderr, "helper: dispatch:", err)
		os.Exit(1)
	}
	fmt.Println("dispatched")
	// A sleeping loop keeps the test binary alive without tripping the runtime's
	// all-goroutines-asleep deadlock detector.
	for {
		time.Sleep(time.Minute)
	}
}

func TestTwoProcessesProduceOneDispatch(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "fleet.db")
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	projectID, err := st.CreateProject(ctx, store.ProjectInput{
		Key: "app", Name: "app", ForgeKind: "github", ForgeProject: "o/r",
		Rig: "app", ConcurrencyCap: 1, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperDispatchHolder$")
	cmd.Env = append(os.Environ(),
		"DBLOCK_HELPER=1",
		"DBLOCK_DB="+db,
		"DBLOCK_PROJECT="+strconv.FormatInt(projectID, 10),
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, err := st.DispatchesForProject(ctx, projectID)
		if err != nil {
			t.Fatalf("read dispatches: %v", err)
		}
		if len(rows) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("helper never dispatched; rows = %d", len(rows))
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The helper is alive and holds the lock: the second process (this one) is
	// refused before it can dispatch.
	if _, err := dblock.Acquire(db); !errors.Is(err, dblock.ErrHeld) {
		t.Fatalf("acquire while the helper runs = %v, want ErrHeld", err)
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// flock dies with the process: no timeout and no stale-lock wait.
	released := false
	deadline = time.Now().Add(2 * time.Second)
	for !released {
		held, err := dblock.Acquire(db)
		if err == nil {
			held.Release()
			released = true
			break
		}
		if !errors.Is(err, dblock.ErrHeld) {
			t.Fatalf("acquire after kill: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("lock still held after the holder was killed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	rows, err := st.DispatchesForProject(ctx, projectID)
	if err != nil {
		t.Fatalf("final read: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("dispatches = %d, want exactly one", len(rows))
	}
}
