// Package queue is a one-file-per-MR job queue with a single-worker lock.
package queue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const MaxAttempts = 3

var ErrBusy = errors.New("another worker is running")

type Job struct {
	Project    string  `json:"project"`
	IID        int     `json:"iid"`
	Source     string  `json:"source"`
	SHA        string  `json:"sha,omitempty"` // head sha when enqueued; parked jobs are keyed by it
	Force      bool    `json:"force"`
	Attempts   int     `json:"attempts"`
	EnqueuedAt float64 `json:"enqueued_at"`
	LastError  string  `json:"last_error,omitempty"`
	path       string
}

type Queue struct {
	dir, failed, lock string
}

func New(root string) (*Queue, error) {
	q := &Queue{dir: filepath.Join(root, "queue"), failed: filepath.Join(root, "queue", "failed"), lock: filepath.Join(root, "worker.lock")}
	return q, os.MkdirAll(q.failed, 0o755)
}

func (q *Queue) file(project string, iid int) string {
	return filepath.Join(q.dir, fmt.Sprintf("%s!%d.json", strings.ReplaceAll(project, "/", "__"), iid))
}

func write(path string, j *Job) error {
	b, _ := json.Marshal(j)
	return os.WriteFile(path, b, 0o644)
}

func read(path string) (*Job, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, err
	}
	j.path = path
	return &j, nil
}

// Enqueue keeps one job per MR; the head sha is re-checked when the job runs.
func (q *Queue) Enqueue(project string, iid int, source, sha string, force bool) error {
	path := q.file(project, iid)
	j := &Job{Project: project, IID: iid, Source: source, SHA: sha, Force: force, EnqueuedAt: float64(time.Now().UnixNano()) / 1e9}
	if old, err := read(path); err == nil {
		j.EnqueuedAt, j.Attempts, j.Force = old.EnqueuedAt, old.Attempts, force || old.Force
	}
	return write(path, j)
}

func (q *Queue) Pending() ([]*Job, error) {
	files, _ := filepath.Glob(filepath.Join(q.dir, "*.json"))
	var jobs []*Job
	for _, f := range files {
		if j, err := read(f); err == nil {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].EnqueuedAt < jobs[b].EnqueuedAt })
	return jobs, nil
}

func (q *Queue) FailedJobs() []*Job {
	files, _ := filepath.Glob(filepath.Join(q.failed, "*.json"))
	var jobs []*Job
	for _, f := range files {
		if j, err := read(f); err == nil {
			jobs = append(jobs, j)
		}
	}
	return jobs
}

func (q *Queue) Done(j *Job) { _ = os.Remove(j.path) }

// Parked reports whether this exact head sha already failed MaxAttempts times, so poll must not
// retry it every cycle. A new push changes the sha and clears the block.
func (q *Queue) Parked(project string, iid int, sha string) bool {
	j, err := read(filepath.Join(q.failed, filepath.Base(q.file(project, iid))))
	return err == nil && j.SHA != "" && j.SHA == sha
}

// Unpark lets a manual run retry a parked MR.
func (q *Queue) Unpark(project string, iid int) {
	_ = os.Remove(filepath.Join(q.failed, filepath.Base(q.file(project, iid))))
}

// Fail keeps the job for retry until MaxAttempts, then parks it in failed/.
func (q *Queue) Fail(j *Job, reason string) {
	j.Attempts++
	if len(reason) > 500 {
		reason = reason[:500]
	}
	j.LastError = reason
	if j.Attempts >= MaxAttempts {
		_ = write(filepath.Join(q.failed, filepath.Base(j.path)), j)
		_ = os.Remove(j.path)
		return
	}
	_ = write(j.path, j)
}

// Lock takes the single-worker lock; the returned func releases it.
func (q *Queue) Lock() (func(), error) {
	f, err := os.OpenFile(q.lock, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
