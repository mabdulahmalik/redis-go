package main

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Store is our key-value database. It holds every key and value saved so far,
// plus an expiration instant for each key that was given one via Expire.
type Store struct {
	mu          sync.Mutex
	data        map[string]string
	expirations map[string]time.Time
}

// NewStore returns a Store that is ready to use. Always use it instead of
// Store{}: the zero value of a map is nil, and writing to a nil map panics.
func NewStore() *Store {
	return &Store{
		data:        make(map[string]string),
		expirations: make(map[string]time.Time),
	}
}

// Get returns the value stored under key, deleting it first if its expiration
// has passed. Returns ErrEmptyKey, or ErrKeyNotFound if the key is gone.
func (s *Store) Get(key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isExpired(key) {
		delete(s.data, key)
		delete(s.expirations, key)
		return "", ErrKeyNotFound
	}

	value, ok := s.data[key]
	if !ok {
		return "", ErrKeyNotFound
	}

	return value, nil
}

// Set stores value under key, overwriting any existing value and clearing any
// expiration on it. If key is empty, it returns ErrEmptyKey and stores nothing.
func (s *Store) Set(key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.data[key] = value
	delete(s.expirations, key)
	return nil
}

// Delete removes key and its expiration, returning ErrEmptyKey if key is
// empty. Deleting a key that does not exist is not an error.
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.data, key)
	delete(s.expirations, key)
	return nil
}

// Keys returns every key currently stored, in no particular order. Expired
// keys are included until a Get, Expire or TTL call removes them.
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]string, 0, len(s.data))
	for key := range s.data {
		keys = append(keys, key)
	}
	return keys
}

// Count returns how many keys are currently stored, expired-but-not-yet
// -removed ones included.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.data)
}

// isExpired reports whether key has an expiration whose instant has already
// passed. A key with no expiration entry is never considered expired.
func (s *Store) isExpired(key string) bool {
	expiresAt, ok := s.expirations[key]
	if !ok {
		return false
	}
	return time.Now().After(expiresAt)
}

// Expire makes key stop existing once ttl has passed, replacing any earlier
// expiration. Returns ErrEmptyKey, or ErrKeyNotFound if key is missing or expired.
func (s *Store) Expire(key string, ttl time.Duration) error {
	if key == "" {
		return ErrEmptyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isExpired(key) {
		delete(s.data, key)
		delete(s.expirations, key)
		return ErrKeyNotFound
	}

	if _, ok := s.data[key]; !ok {
		return ErrKeyNotFound
	}

	s.expirations[key] = time.Now().Add(ttl)
	return nil
}

// TTL returns how much time is left before key expires. Returns ErrEmptyKey,
// ErrKeyNotFound if key is gone, or ErrNoExpiry if it has no expiration.
func (s *Store) TTL(key string) (time.Duration, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isExpired(key) {
		delete(s.data, key)
		delete(s.expirations, key)
		return 0, ErrKeyNotFound
	}

	if _, ok := s.data[key]; !ok {
		return 0, ErrKeyNotFound
	}

	expiresAt, ok := s.expirations[key]
	if !ok {
		return 0, ErrNoExpiry
	}

	return time.Until(expiresAt), nil
}

// Persist cancels any pending expiration on key, making it live forever again.
// Returns ErrEmptyKey, or ErrKeyNotFound if key does not exist.
func (s *Store) Persist(key string) error {
	if key == "" {
		return ErrEmptyKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.data[key]; !ok {
		return ErrKeyNotFound
	}

	delete(s.expirations, key)
	return nil
}

func main() {
	store := NewStore()

	if err := store.Set("name", "gopher"); err != nil {
		fmt.Println("error:", err)
	}

	value, err := store.Get("name")
	if err != nil {
		fmt.Println("error:", err)
	} else {
		fmt.Println(value)
	}

	_, err = store.Get("missing-key")
	if errors.Is(err, ErrKeyNotFound) {
		fmt.Println("that key really doesn't exist")
	}

	store.Set("session", "abc123")
	store.Expire("session", 100*time.Millisecond)

	value, err = store.Get("session")
	fmt.Println(value, err) // abc123 <nil>

	time.Sleep(150 * time.Millisecond)

	_, err = store.Get("session")
	fmt.Println(errors.Is(err, ErrKeyNotFound)) // true
}