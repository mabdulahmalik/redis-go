package main

import "errors"

// Sentinel errors for the store, one per distinguishable failure.
// Callers check them with errors.Is, not by comparing message strings.
var (
	ErrEmptyKey    = errors.New("key must not be empty")
	ErrKeyNotFound = errors.New("key not found")
	ErrNoExpiry    = errors.New("key has no expiration set")
	ErrProtocol    = errors.New("protocol error")
)