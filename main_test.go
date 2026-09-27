package main

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func TestDelete(t *testing.T) {
	store := NewStore()
	store.Set("a", "1")

	store.Delete("a")

	_, err := store.Get("a")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound after deleting 'a', got %v", err)
	}

	// Deleting a key that was never there should not cause a problem.
	if err := store.Delete("does-not-exist"); err != nil {
		t.Errorf("expected deleting a missing key to succeed, got error: %v", err)
	}
}

func TestEmptyKeyValidation(t *testing.T) {
	store := NewStore()

	if err := store.Set("", "value"); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("expected Set(\"\", ...) to return ErrEmptyKey, got %v", err)
	}

	if _, err := store.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("expected Get(\"\") to return ErrEmptyKey, got %v", err)
	}

	if err := store.Delete(""); !errors.Is(err, ErrEmptyKey) {
		t.Errorf("expected Delete(\"\") to return ErrEmptyKey, got %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	store := NewStore()

	_, err := store.Get("never-set")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound for a key that was never set, got %v", err)
	}
}

func TestKeys(t *testing.T) {
	store := NewStore()
	store.Set("charlie", "3")
	store.Set("alpha", "1")
	store.Set("bravo", "2")

	got := store.Keys()
	sort.Strings(got) // map order is random, so we sort before comparing

	want := []string{"alpha", "bravo", "charlie"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
}

func TestKeysOnEmptyStore(t *testing.T) {
	store := NewStore()

	got := store.Keys()

	if len(got) != 0 {
		t.Errorf("expected empty store to have 0 keys, got %d", len(got))
	}
}

func TestCount(t *testing.T) {
	store := NewStore()

	if store.Count() != 0 {
		t.Errorf("expected new store to have Count() == 0, got %d", store.Count())
	}

	store.Set("a", "1")
	store.Set("b", "2")

	if store.Count() != 2 {
		t.Errorf("expected Count() == 2 after 2 sets, got %d", store.Count())
	}

	store.Delete("a")

	if store.Count() != 1 {
		t.Errorf("expected Count() == 1 after deleting one key, got %d", store.Count())
	}
}