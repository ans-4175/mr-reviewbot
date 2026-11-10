package queue

import (
	"errors"
	"testing"
)

func newQ(t *testing.T) *Queue {
	q, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestOneJobPerMRAndForceSticks(t *testing.T) {
	q := newQ(t)
	_ = q.Enqueue("g/p", 1, "poll", "", false)
	_ = q.Enqueue("g/p", 1, "manual", "", true)
	jobs, _ := q.Pending()
	if len(jobs) != 1 || !jobs[0].Force {
		t.Fatalf("%+v", jobs)
	}
}

func TestParkedSHAIsNotRetriedUntilNewPush(t *testing.T) {
	q := newQ(t)
	_ = q.Enqueue("g/p", 3, "poll", "sha-one", false)
	for i := 0; i < MaxAttempts; i++ {
		jobs, _ := q.Pending()
		q.Fail(jobs[0], "boom")
		if i < MaxAttempts-1 && q.Parked("g/p", 3, "sha-one") {
			t.Fatal("parked too early")
		}
	}
	if !q.Parked("g/p", 3, "sha-one") {
		t.Fatal("same sha must stay parked")
	}
	if q.Parked("g/p", 3, "sha-two") {
		t.Fatal("a new push must clear the block")
	}
	q.Unpark("g/p", 3)
	if q.Parked("g/p", 3, "sha-one") {
		t.Fatal("manual unpark must release it")
	}
}

func TestFailsMoveToFailedAfterMaxAttempts(t *testing.T) {
	q := newQ(t)
	_ = q.Enqueue("g/p", 2, "poll", "", false)
	for i := 0; i < MaxAttempts; i++ {
		jobs, _ := q.Pending()
		if len(jobs) != 1 {
			t.Fatalf("attempt %d: %d pending", i, len(jobs))
		}
		q.Fail(jobs[0], "boom")
	}
	if jobs, _ := q.Pending(); len(jobs) != 0 || len(q.FailedJobs()) != 1 {
		t.Errorf("pending=%d failed=%d", len(jobs), len(q.FailedJobs()))
	}
}

func TestSingleWorker(t *testing.T) {
	q := newQ(t)
	unlock, err := q.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Lock(); !errors.Is(err, ErrBusy) {
		t.Errorf("second lock: %v", err)
	}
	unlock()
	if u, err := q.Lock(); err != nil {
		t.Errorf("lock after release: %v", err)
	} else {
		u()
	}
}
