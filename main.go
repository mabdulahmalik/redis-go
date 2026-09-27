package main

import (
	"errors"
	"fmt"
)

// Store is our key-value database. It holds every key and value saved
// so far, in memory, for as long as the program keeps running.
type Store struct {
	data map[string]string
}

// NewStore returns a Store that is ready to use. Always use it instead of
// Store{}: the zero value of a map is nil, and writing to a nil map panics.
func NewStore() *Store {
	return &Store{
		data: make(map[string]string),
	}
}

// Get looks up key and returns its value. It returns ErrEmptyKey if key
// is empty, or ErrKeyNotFound if key is not in the store.
func (s *Store) Get(key string) (string, error) {
	if key == "" {
		return "", ErrEmptyKey
	}

	value, ok := s.data[key]
	if !ok {
		return "", ErrKeyNotFound
	}

	return value, nil
}

// Set stores value under key, overwriting any existing value. If key is
// empty, it returns ErrEmptyKey and stores nothing.
func (s *Store) Set(key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}

	s.data[key] = value
	return nil
}

// Delete removes key from the store, returning ErrEmptyKey if key is empty.
// Deleting a key that does not exist is not an error.
func (s *Store) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}

	delete(s.data, key)
	return nil
}

// Keys returns every key currently stored, in no particular order.
func (s *Store) Keys() []string {
	keys := make([]string, 0, len(s.data))
	for key := range s.data {
		keys = append(keys, key)
	}
	return keys
}

// Count returns how many keys are currently stored.
func (s *Store) Count() int {
	return len(s.data)
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
}