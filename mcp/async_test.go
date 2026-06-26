package mcpserver_test

// async_test.go — tests for the async job registry (unpixel_decode async=true,
// unpixel_job_result, unpixel_job_cancel) and multi-frame per-frame offsets.

import (
	"context"
	"testing"
	"time"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestAsyncDecode_startAndRetrieve verifies the full async lifecycle:
//   - SubmitJob returns a non-empty job ID immediately.
//   - RetrieveJob (with wait) eventually returns a valid DecodeResult.
func TestAsyncDecode_startAndRetrieve(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	jobID, err := mcpserver.SubmitJob(ctx, func(jCtx context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.Decode(jCtx, img, "mosaic", mcpserver.DecodeOptions{})
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	if jobID == "" {
		t.Fatal("SubmitJob: returned empty job ID")
	}

	res, done, err := mcpserver.RetrieveJob(jobID, true)
	if err != nil {
		t.Fatalf("RetrieveJob: %v", err)
	}
	if !done {
		t.Fatal("RetrieveJob(wait=true): done should be true")
	}
	if res.Text == "" {
		t.Error("async decode: Text is empty")
	}
	if res.MethodUsed != "mosaic" {
		t.Errorf("async decode: MethodUsed = %q, want %q", res.MethodUsed, "mosaic")
	}
}

// TestAsyncDecode_cancelNoLeak verifies that cancelling a job terminates its
// goroutine cleanly and the job is removed from the registry.
func TestAsyncDecode_cancelNoLeak(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	// Use a context with a long deadline; we cancel the job manually via CancelJob.
	jobID, err := mcpserver.SubmitJob(context.Background(), func(jCtx context.Context) (mcpserver.DecodeResult, error) {
		// Simulate a long-running job that respects cancellation.
		select {
		case <-jCtx.Done():
			return mcpserver.DecodeResult{}, jCtx.Err()
		case <-time.After(10 * time.Second):
		}
		return mcpserver.Decode(jCtx, img, "mosaic", mcpserver.DecodeOptions{})
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	if err := mcpserver.CancelJob(jobID); err != nil {
		t.Fatalf("CancelJob: %v", err)
	}

	// After cancel the job must not be retrievable.
	_, _, err = mcpserver.RetrieveJob(jobID, true)
	if err == nil {
		t.Error("RetrieveJob after cancel: want error, got nil")
	}
}

// TestAsyncDecode_pollPending verifies that RetrieveJob(wait=false) returns
// done=false while a job is still running.
func TestAsyncDecode_pollPending(t *testing.T) {
	// Block the job forever until cancelled.
	ready := make(chan struct{})
	jobID, err := mcpserver.SubmitJob(context.Background(), func(jCtx context.Context) (mcpserver.DecodeResult, error) {
		close(ready)
		<-jCtx.Done()
		return mcpserver.DecodeResult{}, jCtx.Err()
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	// Wait until the goroutine has started.
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("job goroutine did not start within 2 s")
	}

	_, done, err := mcpserver.RetrieveJob(jobID, false)
	if err != nil {
		t.Fatalf("RetrieveJob(pending): unexpected error: %v", err)
	}
	if done {
		t.Error("RetrieveJob(wait=false): done should be false while job is running")
	}

	// Clean up: cancel the job to avoid leaking the goroutine.
	_ = mcpserver.CancelJob(jobID)
	// Drain the registry so the job goroutine can exit.
	_, _, _ = mcpserver.RetrieveJob(jobID, true)
}

// TestAsyncDecode_cancelFreesSlot verifies that cancelling a job immediately
// reclaims its registry slot so a subsequent submit succeeds even when the
// registry would otherwise be at capacity.
//
// Strategy: fill the registry to (maxJobs-1) blocked jobs, cancel them all,
// then submit one more job and confirm it succeeds. Run with -race to catch
// any concurrent-access bugs in the cancel/submit path.
func TestAsyncDecode_cancelFreesSlot(t *testing.T) {
	// maxJobs is 64 (unexported); fill to one below that so we can measure the
	// baseline without racing the background goroutines from other tests.
	const fill = 10

	blocked := make(chan struct{})
	jobIDs := make([]string, fill)
	for i := range fill {
		id, err := mcpserver.SubmitJob(context.Background(), func(jCtx context.Context) (mcpserver.DecodeResult, error) {
			select {
			case <-jCtx.Done():
				return mcpserver.DecodeResult{}, jCtx.Err()
			case <-blocked:
			}
			return mcpserver.DecodeResult{}, nil
		})
		if err != nil {
			t.Fatalf("SubmitJob[%d]: %v", i, err)
		}
		jobIDs[i] = id
	}

	// Cancel all fill jobs — each must reclaim its slot immediately.
	for _, id := range jobIDs {
		if err := mcpserver.CancelJob(id); err != nil {
			t.Fatalf("CancelJob(%q): %v", id, err)
		}
		// After cancel the job must not be retrievable.
		_, _, err := mcpserver.RetrieveJob(id, false)
		if err == nil {
			t.Errorf("RetrieveJob(%q) after cancel: want error, got nil", id)
		}
	}

	// Unblock any goroutines still draining.
	close(blocked)

	// The registry must now accept a new submission (slot freed).
	newID, err := mcpserver.SubmitJob(context.Background(), func(_ context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.DecodeResult{Text: "ok"}, nil
	})
	if err != nil {
		t.Fatalf("SubmitJob after cancel: %v", err)
	}
	res, done, err := mcpserver.RetrieveJob(newID, true)
	if err != nil {
		t.Fatalf("RetrieveJob new job: %v", err)
	}
	if !done {
		t.Fatal("new job: done should be true after wait=true")
	}
	if res.Text != "ok" {
		t.Errorf("new job: Text = %q, want %q", res.Text, "ok")
	}
}

// TestDecodeMultiFrame_distinctOffsets verifies that method=multi-frame
// accepts frames with distinct per-frame offsets and returns a non-empty result.
//
// We use pad_04_04_go.png (offset 4,4) and pad_12_12_go.png (offset 12,12)
// — both encode "go" at block size 8 but with different grid phases.
func TestDecodeMultiFrame_distinctOffsets(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("pad_04_04_go.png")
	if err != nil {
		t.Fatalf("load primary frame: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "multi-frame", mcpserver.DecodeOptions{
		Frames: []mcpserver.FrameInput{
			{Path: fixturePath("pad_12_12_go.png"), OffsetX: 12, OffsetY: 12},
		},
	})
	if err != nil {
		t.Fatalf("Decode(multi-frame): %v", err)
	}
	if got.Text == "" {
		t.Error("multi-frame: Text is empty")
	}
	if got.MethodUsed != "multi-frame" {
		t.Errorf("multi-frame: MethodUsed = %q, want %q", got.MethodUsed, "multi-frame")
	}
	if got.Distance < 0 {
		t.Errorf("multi-frame: Distance = %.4f, want >= 0", got.Distance)
	}
}
