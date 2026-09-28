package main

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// Run: go test -run TestConcurrentAccess -v ./...   (-v needed to see t.Logf)
func TestConcurrentAccess(t *testing.T) {
	store := NewStore()

	var wg sync.WaitGroup

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", n)
			store.Set(key, "value")
			store.Get(key)
			value, err := store.Get(key)
			if err != nil {
				t.Errorf("Get(%q) returned unexpected error: %v", key, err)
			}
			t.Logf("%s = %s", key, value)
			store.Keys()
			store.Count()
			store.Delete(key)
		}(i)
	}

	wg.Wait()
}

func TestExpireAndGet(t *testing.T) {
	store := NewStore()
	store.Set("session", "abc123")
	store.Expire("session", 50*time.Millisecond)

	// Should still exist immediately after setting the expiration.
	value, err := store.Get("session")
	if err != nil {
		t.Fatalf("expected key to still exist, got error: %v", err)
	}
	if value != "abc123" {
		t.Errorf("expected value 'abc123', got %q", value)
	}

	time.Sleep(100 * time.Millisecond)

	_, err = store.Get("session")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound after expiration, got %v", err)
	}
}

func TestTTL(t *testing.T) {
	store := NewStore()
	store.Set("key", "value")
	store.Expire("key", 1*time.Second)

	remaining, err := store.TTL("key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if remaining <= 0 || remaining > 1*time.Second {
		t.Errorf("expected remaining TTL between 0 and 1s, got %v", remaining)
	}
}

func TestTTLReturnsErrNoExpiryWhenNotSet(t *testing.T) {
	store := NewStore()
	store.Set("key", "value")

	_, err := store.TTL("key")
	if !errors.Is(err, ErrNoExpiry) {
		t.Errorf("expected ErrNoExpiry, got %v", err)
	}
}

func TestExpireOnMissingKeyReturnsError(t *testing.T) {
	store := NewStore()

	err := store.Expire("does-not-exist", 1*time.Second)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("expected ErrKeyNotFound, got %v", err)
	}
}

func TestPersistRemovesExpiration(t *testing.T) {
	store := NewStore()
	store.Set("key", "value")
	store.Expire("key", 50*time.Millisecond)

	if err := store.Persist("key"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	value, err := store.Get("key")
	if err != nil {
		t.Fatalf("expected key to survive past its old expiration, got error: %v", err)
	}
	if value != "value" {
		t.Errorf("expected value 'value', got %q", value)
	}
}

func TestSetClearsExistingExpiration(t *testing.T) {
	store := NewStore()
	store.Set("key", "value")
	store.Expire("key", 1*time.Second)

	// Overwriting with Set should remove the old expiration,
	// matching real Redis's behavior for a plain SET.
	store.Set("key", "new-value")

	_, err := store.TTL("key")
	if !errors.Is(err, ErrNoExpiry) {
		t.Errorf("expected ErrNoExpiry after re-Set, got %v", err)
	}
}

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