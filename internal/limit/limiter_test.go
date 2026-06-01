package limit

import (
	"context"
	"testing"
	"time"
)

func TestLimiterRejectsWhenConcurrencyExceededWithoutQueue(t *testing.T) {
	lim := New(Config{MaxConcurrent: 1})
	lease, reason := lim.Acquire(context.Background(), "global")
	if reason != RejectNone || lease == nil {
		t.Fatalf("first Acquire reason=%q lease=%v, want lease", reason, lease)
	}
	defer lease.Release()

	if gotLease, gotReason := lim.Acquire(context.Background(), "global"); gotLease != nil || gotReason != RejectConcurrencyExceeded {
		t.Fatalf("second Acquire lease=%v reason=%q, want concurrency rejection", gotLease, gotReason)
	}
}

func TestLimiterQueuesAndAcquiresAfterRelease(t *testing.T) {
	lim := New(Config{MaxConcurrent: 1, MaxQueued: 1})
	lease, reason := lim.Acquire(context.Background(), "channel-a")
	if reason != RejectNone || lease == nil {
		t.Fatalf("first Acquire reason=%q lease=%v, want lease", reason, lease)
	}

	result := make(chan RejectReason, 1)
	released := make(chan struct{})
	go func() {
		queuedLease, queuedReason := lim.Acquire(context.Background(), "channel-a")
		result <- queuedReason
		if queuedLease != nil {
			queuedLease.Release()
		}
		close(released)
	}()

	select {
	case reason := <-result:
		t.Fatalf("queued Acquire returned before release with reason=%q", reason)
	case <-time.After(20 * time.Millisecond):
	}

	lease.Release()
	select {
	case reason := <-result:
		if reason != RejectNone {
			t.Fatalf("queued Acquire reason=%q, want none", reason)
		}
	case <-time.After(time.Second):
		t.Fatalf("queued Acquire did not return after release")
	}
	<-released
}

func TestLimiterRejectsWhenQueueSaturated(t *testing.T) {
	lim := New(Config{MaxConcurrent: 1, MaxQueued: 1})
	lease, reason := lim.Acquire(context.Background(), "channel-a")
	if reason != RejectNone || lease == nil {
		t.Fatalf("first Acquire reason=%q lease=%v, want lease", reason, lease)
	}
	defer lease.Release()

	queuedStarted := make(chan struct{})
	go func() {
		close(queuedStarted)
		queuedLease, _ := lim.Acquire(context.Background(), "channel-a")
		if queuedLease != nil {
			queuedLease.Release()
		}
	}()
	<-queuedStarted
	time.Sleep(20 * time.Millisecond)

	if gotLease, gotReason := lim.Acquire(context.Background(), "channel-a"); gotLease != nil || gotReason != RejectQueueSaturated {
		t.Fatalf("Acquire lease=%v reason=%q, want queue saturated", gotLease, gotReason)
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	lim := New(Config{MaxConcurrent: 1})
	lease, reason := lim.Acquire(context.Background(), "channel-a")
	if reason != RejectNone || lease == nil {
		t.Fatalf("first Acquire reason=%q lease=%v, want lease", reason, lease)
	}
	defer lease.Release()

	otherLease, otherReason := lim.Acquire(context.Background(), "channel-b")
	if otherReason != RejectNone || otherLease == nil {
		t.Fatalf("other key Acquire reason=%q lease=%v, want lease", otherReason, otherLease)
	}
	otherLease.Release()
}

func TestLimiterBlankKeyUsesGlobalBucket(t *testing.T) {
	lim := New(Config{MaxConcurrent: 1})
	lease, reason := lim.Acquire(context.Background(), "")
	if reason != RejectNone || lease == nil {
		t.Fatalf("blank-key Acquire reason=%q lease=%v, want lease", reason, lease)
	}
	defer lease.Release()

	if gotLease, gotReason := lim.Acquire(context.Background(), "global"); gotLease != nil || gotReason != RejectConcurrencyExceeded {
		t.Fatalf("global Acquire lease=%v reason=%q, want same bucket rejection", gotLease, gotReason)
	}
}
