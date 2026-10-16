package config

import (
	"errors"
	"testing"
	"time"
)

var errReaderHoldsFile = errors.New("the process cannot access the file")

func TestReplaceWithRetry_RetriesWhileAReaderHoldsTheTarget(t *testing.T) {
	calls := 0
	var slept []time.Duration
	err := replaceWithRetry(
		func(string, string) error {
			calls++
			if calls < 3 {
				return errReaderHoldsFile
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, errReaderHoldsFile) },
		func(d time.Duration) { slept = append(slept, d) },
		"hosts.json.tmp", "hosts.json",
	)
	if err != nil {
		t.Fatalf("replace after two blocked attempts: %v", err)
	}
	if calls != 3 {
		t.Errorf("rename attempts = %d, want 3", calls)
	}
	if len(slept) != 2 || slept[0] != replaceRetryFirst || slept[1] != 2*replaceRetryFirst {
		t.Errorf("backoff = %v, want [%v %v]", slept, replaceRetryFirst, 2*replaceRetryFirst)
	}
}

func TestReplaceWithRetry_ReturnsAPermanentErrorAtOnce(t *testing.T) {
	permanent := errors.New("disk full")
	calls := 0
	slept := false
	err := replaceWithRetry(
		func(string, string) error { calls++; return permanent },
		func(err error) bool { return errors.Is(err, errReaderHoldsFile) },
		func(time.Duration) { slept = true },
		"hosts.json.tmp", "hosts.json",
	)
	if !errors.Is(err, permanent) || calls != 1 || slept {
		t.Errorf("permanent error: err=%v calls=%d slept=%v, want the error after one attempt without waiting", err, calls, slept)
	}
}

func TestReplaceWithRetry_GivesUpAfterItsBudget(t *testing.T) {
	calls := 0
	var waited time.Duration
	err := replaceWithRetry(
		func(string, string) error { calls++; return errReaderHoldsFile },
		func(err error) bool { return errors.Is(err, errReaderHoldsFile) },
		func(d time.Duration) { waited += d },
		"hosts.json.tmp", "hosts.json",
	)
	if !errors.Is(err, errReaderHoldsFile) {
		t.Fatalf("a target that stays blocked must surface the last error, got %v", err)
	}
	if waited < replaceRetryBudget || waited > replaceRetryBudget+replaceRetryMax {
		t.Errorf("waited %v in total, want the %v budget plus at most one %v step", waited, replaceRetryBudget, replaceRetryMax)
	}
	if calls < 2 || calls > 30 {
		t.Errorf("rename attempts = %d, want a bounded retry", calls)
	}
}
