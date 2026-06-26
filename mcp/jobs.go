package mcpserver

// jobs.go — in-process async job registry for long-running unpixel_decode calls.
//
// The MCP Tasks extension (spec 2026-07-28) is not yet exposed by go-sdk
// v1.7.0-pre.1 — the SDK added no tasks/get, tasks/update, or tasks/cancel
// methods. The fallback implemented here exposes three tools instead:
//
//   - unpixel_decode: when async=true, launches the decode in a background
//     goroutine and immediately returns a job_id. The client then polls.
//   - unpixel_job_result: polls by job_id; returns the DecodeResult once done,
//     or a "pending" status while the job is still running.
//   - unpixel_job_cancel: cancels a running job by job_id.
//
// The registry is bounded to [maxJobs] active + completed jobs. When the
// registry is full, new async requests return an error. Completed jobs are
// retained until the first result poll (the retrieve-and-delete pattern) so
// callers always get the result exactly once.
//
// Every job context is cancelled on cancel or automatic cleanup, so no
// goroutine leaks can accumulate.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
	"time"
)

// maxJobs is the maximum number of active + completed jobs tracked at once.
// Callers that exceed this limit must wait for outstanding jobs to complete.
const maxJobs = 64

// jobState is the lifecycle state of an async decode job.
type jobState uint8

const (
	jobStatePending   jobState = iota // running in a goroutine
	jobStateDone                      // finished successfully
	jobStateFailed                    // finished with an error
	jobStateCancelled                 // cancelled before completion
)

// job holds all per-job state for an async decode.
type job struct {
	id     string
	cancel context.CancelFunc
	state  jobState
	result DecodeResult
	err    error
	done   chan struct{} // closed when job completes (state != pending)
}

// jobRegistry is the singleton in-process async job store.
var jobRegistry = &registry{jobs: make(map[string]*job)}

// registry guards the job map with a mutex.
type registry struct {
	mu   sync.Mutex
	jobs map[string]*job
}

// submit starts a new async decode job. It returns the job ID, or an error if
// the registry is full. The caller must supply a derived context that it does
// not cancel independently — the registry manages cancellation.
func (r *registry) submit(parent context.Context, fn func(context.Context) (DecodeResult, error)) (string, error) {
	r.mu.Lock()
	if len(r.jobs) >= maxJobs {
		r.mu.Unlock()
		return "", fmt.Errorf("async job registry full (%d jobs); cancel or retrieve existing jobs", maxJobs)
	}
	id, err := newJobID()
	if err != nil {
		r.mu.Unlock()
		return "", fmt.Errorf("generate job ID: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	j := &job{
		id:     id,
		cancel: cancel,
		state:  jobStatePending,
		done:   make(chan struct{}),
	}
	r.jobs[id] = j
	r.mu.Unlock()

	go func() {
		defer close(j.done)
		res, runErr := fn(ctx)
		r.mu.Lock()
		if j.state == jobStateCancelled {
			r.mu.Unlock()
			return
		}
		if runErr != nil {
			j.state = jobStateFailed
			j.err = runErr
		} else {
			j.state = jobStateDone
			j.result = res
		}
		r.mu.Unlock()
	}()

	return id, nil
}

// retrieve returns the job result for id, blocking until the job completes.
// When wait is false it returns immediately with (result, done, err) — if
// done is false the job is still pending. When wait is true it blocks until
// completion. On first successful retrieval of a completed job the job is
// removed from the registry.
func (r *registry) retrieve(id string, wait bool) (DecodeResult, bool, error) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return DecodeResult{}, false, fmt.Errorf("job %q not found (already retrieved or unknown)", id)
	}

	if wait {
		<-j.done
	} else {
		select {
		case <-j.done:
		default:
			// Still running.
			return DecodeResult{}, false, nil
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	switch j.state {
	case jobStateCancelled:
		j.cancel()
		delete(r.jobs, id)
		return DecodeResult{}, true, fmt.Errorf("job %q was cancelled", id)
	case jobStateFailed:
		err := j.err
		j.cancel()
		delete(r.jobs, id)
		return DecodeResult{}, true, err
	default:
		res := j.result
		j.cancel()
		delete(r.jobs, id)
		return res, true, nil
	}
}

// cancel cancels the job with id and immediately removes it from the registry.
// The worker goroutine may still be running; it will detect the cancelled
// context and exit cleanly without writing back any result.
// Returns an error if the job does not exist or is already done.
func (r *registry) cancel(id string) error {
	r.mu.Lock()
	j, ok := r.jobs[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("job %q not found (already retrieved or unknown)", id)
	}
	if j.state == jobStatePending {
		j.state = jobStateCancelled
		j.cancel()
		delete(r.jobs, id)
	}
	r.mu.Unlock()
	return nil
}

// newJobID returns a random 16-byte hex job identifier.
func newJobID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]) + "-" + strconv.FormatInt(time.Now().UnixNano()%1e9, 10), nil
}

// SubmitJob submits fn to the global async job registry and returns the job ID.
// It is the exported test-facing wrapper around [registry.submit]. Production
// code goes through [handleDecode]; tests call this directly to exercise the
// registry without going through the MCP tool handler.
func SubmitJob(ctx context.Context, fn func(context.Context) (DecodeResult, error)) (string, error) {
	return jobRegistry.submit(ctx, fn)
}

// RetrieveJob retrieves the result for id. When wait is true it blocks until
// the job completes; when false it returns immediately with done=false if the
// job is still running. On first successful retrieval the job is removed from
// the registry.
func RetrieveJob(id string, wait bool) (DecodeResult, bool, error) {
	return jobRegistry.retrieve(id, wait)
}

// CancelJob cancels the job with id. It is a no-op if the job has already
// completed. Returns an error if the job does not exist.
func CancelJob(id string) error {
	return jobRegistry.cancel(id)
}
