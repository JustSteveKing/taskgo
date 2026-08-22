package claim

import (
	"testing"
	"time"

	"github.com/JustSteveKing/taskgo/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return s
}

func TestTakeAndLoad(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := Take(s, 1, "claude-code", "sess-a", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}

	set, err := Load(s, now)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c, ok := set.Get(1)
	if !ok {
		t.Fatal("claim not found")
	}
	if c.By != "claude-code" || !c.Explicit {
		t.Errorf("claim = %+v", c)
	}
}

// The whole reason a claim is a lease: an agent that dies must stop being
// shown as busy without anyone intervening.
func TestClaimExpires(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := Take(s, 1, "agent", "sess", time.Minute, false, now); err != nil {
		t.Fatalf("Take: %v", err)
	}

	set, _ := Load(s, now.Add(30*time.Second))
	if _, ok := set.Get(1); !ok {
		t.Error("claim should still be held after 30s of a 1m lease")
	}

	set, _ = Load(s, now.Add(2*time.Minute))
	if _, ok := set.Get(1); ok {
		t.Error("claim should have expired after 2m of a 1m lease")
	}
}

// Renewing must not reset the start time, or "held for 40 minutes" becomes a
// lie that resets on every write.
func TestRenewKeepsTheOriginalStartTime(t *testing.T) {
	s := newStore(t)
	start := time.Now()

	if _, err := Take(s, 1, "agent", "sess", DefaultTTL, false, start); err != nil {
		t.Fatalf("Take: %v", err)
	}

	// Renew inside the lease window.
	later := start.Add(2 * time.Minute)
	c, err := Take(s, 1, "agent", "sess", DefaultTTL, false, later)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}

	if !c.Since.Equal(start) {
		t.Errorf("Since = %v, want the original %v", c.Since, start)
	}
	if !c.Expires.After(later) {
		t.Error("renewing should have extended the lease")
	}
	if got := c.Held(later); got < 2*time.Minute {
		t.Errorf("Held = %v, want at least 2m", got)
	}
}

// Renewing after the lease lapsed is a NEW claim, and its clock starts now.
// Carrying the old start time across a gap would report the agent as having
// held the task throughout a period when it was demonstrably absent.
func TestClaimAfterExpiryRestartsTheClock(t *testing.T) {
	s := newStore(t)
	start := time.Now()

	if _, err := Take(s, 1, "agent", "sess", time.Minute, false, start); err != nil {
		t.Fatalf("Take: %v", err)
	}

	afterGap := start.Add(30 * time.Minute)
	c, err := Take(s, 1, "agent", "sess", DefaultTTL, false, afterGap)
	if err != nil {
		t.Fatalf("re-take: %v", err)
	}
	if !c.Since.Equal(afterGap) {
		t.Errorf("Since = %v, want the new start %v", c.Since, afterGap)
	}
}

// An explicit claim must not be silently downgraded by a later implicit write.
func TestImplicitRenewalKeepsExplicitFlag(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := Take(s, 1, "agent", "sess", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}
	c, err := Take(s, 1, "agent", "sess", DefaultTTL, false, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !c.Explicit {
		t.Error("an implicit write downgraded an explicit claim")
	}
}

// Two agents on one task is worth surfacing, not papering over.
func TestSecondSessionCannotStealALiveClaim(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := Take(s, 1, "agent-a", "sess-a", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}
	_, err := Take(s, 1, "agent-b", "sess-b", ExplicitTTL, true, now)
	if err == nil {
		t.Fatal("expected a conflict")
	}

	// ...but once the first lease lapses, the task is free.
	if _, err := Take(s, 1, "agent-b", "sess-b", ExplicitTTL, true, now.Add(ExplicitTTL+time.Minute)); err != nil {
		t.Errorf("an expired claim should not block a new one: %v", err)
	}
}

func TestReleaseOnlyAffectsYourOwnClaim(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := Take(s, 1, "agent-a", "sess-a", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}

	// A different session releasing must be a no-op, not a theft.
	if err := Release(s, 1, "sess-b", now); err != nil {
		t.Fatalf("Release: %v", err)
	}
	set, _ := Load(s, now)
	if _, ok := set.Get(1); !ok {
		t.Error("another session released a claim it did not hold")
	}

	if err := Release(s, 1, "sess-a", now); err != nil {
		t.Fatalf("Release: %v", err)
	}
	set, _ = Load(s, now)
	if _, ok := set.Get(1); ok {
		t.Error("the owner's release did not take effect")
	}
}

// This is what makes the common case work without heartbeats.
func TestReleaseSessionDropsEverythingThatSessionHeld(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	for _, id := range []int{1, 2, 3} {
		if _, err := Take(s, id, "agent-a", "sess-a", ExplicitTTL, true, now); err != nil {
			t.Fatalf("Take %d: %v", id, err)
		}
	}
	if _, err := Take(s, 4, "agent-b", "sess-b", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take 4: %v", err)
	}

	if n := ReleaseSession(s, "sess-a", now); n != 3 {
		t.Errorf("released %d, want 3", n)
	}

	set, _ := Load(s, now)
	if len(set) != 1 {
		t.Fatalf("want only the other session's claim left, got %+v", set)
	}
	if _, ok := set.Get(4); !ok {
		t.Error("released another session's claim")
	}
}

// Completing a task ends the work whoever was doing it.
func TestReleaseTaskIgnoresOwnership(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	if _, err := Take(s, 1, "agent-a", "sess-a", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}
	ReleaseTask(s, 1, now)

	set, _ := Load(s, now)
	if _, ok := set.Get(1); ok {
		t.Error("ReleaseTask left the claim in place")
	}
}

// Expired entries must not accumulate in the file forever.
func TestWritesPruneExpiredEntries(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	for _, id := range []int{1, 2, 3} {
		if _, err := Take(s, id, "old", "sess-old", time.Minute, false, now); err != nil {
			t.Fatalf("Take: %v", err)
		}
	}

	later := now.Add(time.Hour)
	if _, err := Take(s, 9, "new", "sess-new", DefaultTTL, false, later); err != nil {
		t.Fatalf("Take: %v", err)
	}

	raw, err := readFile(s.Root())
	if err != nil {
		t.Fatalf("readFile: %v", err)
	}
	if len(raw.Claims) != 1 {
		t.Errorf("expired entries were not pruned on write: %+v", raw.Claims)
	}
}

func TestTouchNeverFails(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	// Someone else holds it; Touch must not panic or block, because it runs
	// after a write that already succeeded.
	if _, err := Take(s, 1, "agent-a", "sess-a", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}
	Touch(s, 1, "agent-b", "sess-b", now)

	set, _ := Load(s, now)
	if c, _ := set.Get(1); c.By != "agent-a" {
		t.Errorf("Touch stole a live claim: %+v", c)
	}
}

func TestSortedIsOldestFirst(t *testing.T) {
	s := newStore(t)
	now := time.Now()

	// Both must still be live at `now`, so the older one is offset by less
	// than its TTL.
	if _, err := Take(s, 1, "a", "s1", ExplicitTTL, true, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("Take: %v", err)
	}
	if _, err := Take(s, 2, "b", "s2", ExplicitTTL, true, now); err != nil {
		t.Fatalf("Take: %v", err)
	}

	set, _ := Load(s, now)
	sorted := set.Sorted()
	if len(sorted) != 2 || sorted[0].TaskID != 1 {
		t.Errorf("want the oldest claim first, got %+v", sorted)
	}
}
