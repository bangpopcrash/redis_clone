// Package store holds the in-memory key-value engine. This package does
// not know about the network or the RESP protocol. A caller passes Go
// values in. A caller gets Go values, or an error, back.
package store

import (
	"errors"
	"sync"
	"time"
)

var ErrWrongType = errors.New("store: value at key is not the requested type")

type entry struct {
	value any // A string value or a map[string]string value.
	// expiresAt holds the zero Time value when the key has no expiry.
	expiresAt time.Time
}

func (e entry) expired(now time.Time) bool {
	return !e.expiresAt.IsZero() && now.After(e.expiresAt)
}

// Store is an in-memory key-value map. A mutex protects the map. All
// methods on Store are safe to call from more than one goroutine at a
// time.
type Store struct {
	mu   sync.Mutex
	data map[string]entry
	now  func() time.Time // A test can replace this function.
}

func New() *Store {
	return &Store{
		data: make(map[string]entry),
		now:  time.Now,
	}
}

// getLocked returns the entry for key if the entry has not expired. If the
// entry has expired, getLocked deletes it first and returns false. The
// caller must hold s.mu before it calls getLocked.
func (s *Store) getLocked(key string) (entry, bool) {
	e, ok := s.data[key]
	if !ok {
		return entry{}, false
	}
	if e.expired(s.now()) {
		delete(s.data, key)
		return entry{}, false
	}
	return e, true
}

// Set stores value as a string at key. Set removes any TTL that key had
// before.
func (s *Store) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = entry{value: value}
}

// Get returns the string value at key. ok is false when the key does not
// exist or has expired. err is ErrWrongType when the key holds a value
// that is not a string.
func (s *Store) Get(key string) (value string, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	if !found {
		return "", false, nil
	}
	str, isStr := e.value.(string)
	if !isStr {
		return "", false, ErrWrongType
	}
	return str, true, nil
}

// Del deletes each key in keys. Del returns the count of keys that
// existed and had not expired.
func (s *Store) Del(keys ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, k := range keys {
		if _, found := s.getLocked(k); found {
			delete(s.data, k)
			count++
		}
	}
	return count
}

// Exists returns the count of keys in keys that exist now and have not
// expired. As in real Redis, Exists counts the same key more than once if
// keys lists it more than once.
func (s *Store) Exists(keys ...string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, k := range keys {
		if _, found := s.getLocked(k); found {
			count++
		}
	}
	return count
}

// Expire sets key to expire after the time in ttl passes. Expire returns
// false if the key does not exist.
func (s *Store) Expire(key string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	if !found {
		return false
	}
	e.expiresAt = s.now().Add(ttl)
	s.data[key] = e
	return true
}

// TTL returns the time left before key expires. found is false when the
// key does not exist. hasExpiry is false when the key exists but has no
// expiry set.
func (s *Store) TTL(key string) (ttl time.Duration, found bool, hasExpiry bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.getLocked(key)
	if !ok {
		return 0, false, false
	}
	if e.expiresAt.IsZero() {
		return 0, true, false
	}
	return e.expiresAt.Sub(s.now()), true, true
}

// Persist removes any TTL from key. Persist returns true when it removes
// a TTL.
func (s *Store) Persist(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	if !found || e.expiresAt.IsZero() {
		return false
	}
	e.expiresAt = time.Time{}
	s.data[key] = e
	return true
}

// HSet sets field to value in the hash at key. If the hash at key does not
// exist, HSet creates it. HSet returns true when field is new.
func (s *Store) HSet(key, field, value string) (isNewField bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	var hash map[string]string
	if found {
		h, isHash := e.value.(map[string]string)
		if !isHash {
			return false, ErrWrongType
		}
		hash = h
	} else {
		hash = make(map[string]string)
		e = entry{value: hash}
	}
	_, existed := hash[field]
	hash[field] = value
	s.data[key] = e
	return !existed, nil
}

// HGet returns the value of field from the hash at key.
func (s *Store) HGet(key, field string) (value string, ok bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	if !found {
		return "", false, nil
	}
	hash, isHash := e.value.(map[string]string)
	if !isHash {
		return "", false, ErrWrongType
	}
	v, ok := hash[field]
	return v, ok, nil
}

// HDel deletes each field in fields from the hash at key. HDel returns the
// count of fields that existed.
func (s *Store) HDel(key string, fields ...string) (count int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	if !found {
		return 0, nil
	}
	hash, isHash := e.value.(map[string]string)
	if !isHash {
		return 0, ErrWrongType
	}
	for _, f := range fields {
		if _, ok := hash[f]; ok {
			delete(hash, f)
			count++
		}
	}
	if len(hash) == 0 {
		delete(s.data, key)
	}
	return count, nil
}

// HGetAll returns a copy of every field and value pair in the hash at key.
func (s *Store) HGetAll(key string) (fields map[string]string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, found := s.getLocked(key)
	if !found {
		return map[string]string{}, nil
	}
	hash, isHash := e.value.(map[string]string)
	if !isHash {
		return nil, ErrWrongType
	}
	out := make(map[string]string, len(hash))
	for k, v := range hash {
		out[k] = v
	}
	return out, nil
}
