package aof

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestReplayMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.aof")
	called := false
	err := Replay(path, func(args []string) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Replay() on missing file returned error: %v", err)
	}
	if called {
		t.Fatal("apply was called on a missing file")
	}
}

func TestAppendAndReplayRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")

	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open() unexpected error: %v", err)
	}

	writes := [][]string{
		{"SET", "key1", "value1"},
		{"SET", "key2", "value2"},
		{"HSET", "hash", "field", "value"},
		{"DEL", "key1"},
	}
	for _, args := range writes {
		if err := log.Append(args); err != nil {
			t.Fatalf("Append(%v) unexpected error: %v", args, err)
		}
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}

	var replayed [][]string
	err = Replay(path, func(args []string) error {
		cp := append([]string(nil), args...)
		replayed = append(replayed, cp)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay() unexpected error: %v", err)
	}

	if len(replayed) != len(writes) {
		t.Fatalf("Replay() applied %d entries, want %d", len(replayed), len(writes))
	}
	for i, want := range writes {
		got := replayed[i]
		if len(got) != len(want) {
			t.Fatalf("entry %d = %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("entry %d = %v, want %v", i, got, want)
			}
		}
	}
}

func TestReplayPropagatesApplyError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")

	log, err := Open(path)
	if err != nil {
		t.Fatalf("Open() unexpected error: %v", err)
	}
	if err := log.Append([]string{"SET", "key", "value"}); err != nil {
		t.Fatalf("Append() unexpected error: %v", err)
	}
	if err := log.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}

	sentinel := errors.New("boom")
	err = Replay(path, func(args []string) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Replay() error = %v, want wrapping %v", err, sentinel)
	}
}

func TestAppendAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.aof")

	log1, err := Open(path)
	if err != nil {
		t.Fatalf("Open() unexpected error: %v", err)
	}
	if err := log1.Append([]string{"SET", "a", "1"}); err != nil {
		t.Fatalf("Append() unexpected error: %v", err)
	}
	if err := log1.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}

	log2, err := Open(path)
	if err != nil {
		t.Fatalf("Open() (reopen) unexpected error: %v", err)
	}
	if err := log2.Append([]string{"SET", "b", "2"}); err != nil {
		t.Fatalf("Append() unexpected error: %v", err)
	}
	if err := log2.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}

	var replayed [][]string
	err = Replay(path, func(args []string) error {
		replayed = append(replayed, args)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay() unexpected error: %v", err)
	}
	if len(replayed) != 2 {
		t.Fatalf("Replay() applied %d entries across reopen, want 2", len(replayed))
	}
}
