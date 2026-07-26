package store

import (
	"errors"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New()

	if _, ok, _ := s.Get("missing"); ok {
		t.Fatal("Get() on missing key returned ok=true")
	}

	s.Set("key", "value")
	got, ok, err := s.Get("key")
	if err != nil || !ok {
		t.Fatalf("Get() = %q, %v, %v; want value, true, nil", got, ok, err)
	}
	if got != "value" {
		t.Fatalf("Get() = %q, want %q", got, "value")
	}

	s.Set("key", "overwritten")
	got, _, _ = s.Get("key")
	if got != "overwritten" {
		t.Fatalf("Get() after overwrite = %q, want %q", got, "overwritten")
	}
}

func TestGetWrongType(t *testing.T) {
	s := New()
	if _, err := s.HSet("key", "field", "value"); err != nil {
		t.Fatalf("HSet() unexpected error: %v", err)
	}
	if _, _, err := s.Get("key"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("Get() on hash key error = %v, want %v", err, ErrWrongType)
	}
}

func TestDel(t *testing.T) {
	s := New()
	s.Set("a", "1")
	s.Set("b", "2")

	if n := s.Del("a", "b", "missing"); n != 2 {
		t.Fatalf("Del() = %d, want 2", n)
	}
	if _, ok, _ := s.Get("a"); ok {
		t.Fatal("key 'a' still present after Del()")
	}
}

func TestExists(t *testing.T) {
	s := New()
	s.Set("a", "1")

	if n := s.Exists("a", "a", "missing"); n != 2 {
		t.Fatalf("Exists() = %d, want 2 (repeated key counted twice)", n)
	}
}

func TestExpireAndTTL(t *testing.T) {
	s := New()
	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixedNow }

	s.Set("key", "value")

	if _, found, _ := s.TTL("nonexistent"); found {
		t.Fatal("TTL() found=true on missing key")
	}

	if ok := s.Expire("missing", time.Minute); ok {
		t.Fatal("Expire() on missing key returned true")
	}

	if ok := s.Expire("key", time.Minute); !ok {
		t.Fatal("Expire() on existing key returned false")
	}

	ttl, found, hasExpiry := s.TTL("key")
	if !found || !hasExpiry {
		t.Fatalf("TTL() = _, found=%v, hasExpiry=%v; want true, true", found, hasExpiry)
	}
	if ttl != time.Minute {
		t.Fatalf("TTL() = %v, want %v", ttl, time.Minute)
	}

	// Advance past expiry.
	s.now = func() time.Time { return fixedNow.Add(2 * time.Minute) }
	if _, ok, _ := s.Get("key"); ok {
		t.Fatal("Get() returned expired key as present")
	}
}

func TestPersist(t *testing.T) {
	s := New()
	s.Set("key", "value")

	if ok := s.Persist("key"); ok {
		t.Fatal("Persist() on key with no TTL returned true")
	}

	s.Expire("key", time.Minute)
	if ok := s.Persist("key"); !ok {
		t.Fatal("Persist() on key with TTL returned false")
	}

	_, _, hasExpiry := s.TTL("key")
	if hasExpiry {
		t.Fatal("key still has expiry after Persist()")
	}
}

func TestHashCommands(t *testing.T) {
	s := New()

	isNew, err := s.HSet("h", "f1", "v1")
	if err != nil || !isNew {
		t.Fatalf("HSet() = %v, %v; want true, nil", isNew, err)
	}

	isNew, err = s.HSet("h", "f1", "v2")
	if err != nil || isNew {
		t.Fatalf("HSet() on existing field = %v, %v; want false, nil", isNew, err)
	}

	v, ok, err := s.HGet("h", "f1")
	if err != nil || !ok || v != "v2" {
		t.Fatalf("HGet() = %q, %v, %v; want v2, true, nil", v, ok, err)
	}

	if _, ok, _ := s.HGet("h", "missing"); ok {
		t.Fatal("HGet() on missing field returned ok=true")
	}

	if _, err := s.HSet("h", "f2", "v3"); err != nil {
		t.Fatalf("HSet() unexpected error: %v", err)
	}
	all, err := s.HGetAll("h")
	if err != nil {
		t.Fatalf("HGetAll() unexpected error: %v", err)
	}
	want := map[string]string{"f1": "v2", "f2": "v3"}
	if len(all) != len(want) || all["f1"] != want["f1"] || all["f2"] != want["f2"] {
		t.Fatalf("HGetAll() = %v, want %v", all, want)
	}

	n, err := s.HDel("h", "f1", "missing")
	if err != nil || n != 1 {
		t.Fatalf("HDel() = %d, %v; want 1, nil", n, err)
	}

	// HDel removes the key when it deletes the last field.
	if _, err := s.HDel("h", "f2"); err != nil {
		t.Fatalf("HDel() unexpected error: %v", err)
	}
	if n := s.Exists("h"); n != 0 {
		t.Fatal("hash key still exists after all fields deleted")
	}
}

func TestHashWrongType(t *testing.T) {
	s := New()
	s.Set("key", "string value")

	if _, err := s.HSet("key", "f", "v"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("HSet() on string key error = %v, want %v", err, ErrWrongType)
	}
	if _, _, err := s.HGet("key", "f"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("HGet() on string key error = %v, want %v", err, ErrWrongType)
	}
	if _, err := s.HDel("key", "f"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("HDel() on string key error = %v, want %v", err, ErrWrongType)
	}
	if _, err := s.HGetAll("key"); !errors.Is(err, ErrWrongType) {
		t.Fatalf("HGetAll() on string key error = %v, want %v", err, ErrWrongType)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New()
	const goroutines = 50
	const opsPerGoroutine = 200

	done := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < opsPerGoroutine; j++ {
				s.Set("shared", "value")
				_, _, _ = s.Get("shared")
				_, _ = s.HSet("sharedhash", "field", "value")
				_, _, _ = s.HGet("sharedhash", "field")
				s.Exists("shared")
			}
		}(i)
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
}
