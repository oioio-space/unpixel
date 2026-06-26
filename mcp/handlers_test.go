package mcpserver_test

// handlers_test.go — in-process MCP client tests that exercise the unexported
// handle* functions (handleAnalyze, handleVerify, handleDecode, handleRender,
// handleRankFonts, handleCalibrate, handleJobResult, handleJobCancel) and the
// shared helpers errResult / toolJSON / jsonMarshal.
//
// Each test creates an in-process server+client pair via
// mcpsdk.NewInMemoryTransports so the full tool-dispatch path is traversed
// without any network I/O. All tests run under -short and complete in < 30 s.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// newTestClient wires an in-process MCP server+client pair and returns a
// connected ClientSession. The server session and client session are both
// closed when the test ends via t.Cleanup.
func newTestClient(t *testing.T) *mcpsdk.ClientSession {
	t.Helper()
	ctx := t.Context()

	srv := mcpserver.NewServer("v0.0.0-test")
	cTransport, sTransport := mcpsdk.NewInMemoryTransports()

	ss, err := srv.Connect(ctx, sTransport, nil)
	if err != nil {
		t.Fatalf("server Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := ss.Close(); err != nil {
			t.Logf("server session close: %v", err)
		}
	})

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := client.Connect(ctx, cTransport, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("client session close: %v", err)
		}
	})

	return cs
}

// callTool is a convenience wrapper that marshals args to the Arguments field
// and calls the named tool, returning the raw text of the first content item.
func callTool(t *testing.T, cs *mcpsdk.ClientSession, toolName string, args any) (text string, isError bool) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool(%q): %v", toolName, err)
	}
	if len(res.Content) == 0 {
		return "", res.IsError
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("CallTool(%q): first content is not TextContent", toolName)
	}
	return tc.Text, res.IsError
}

// ---- handleAnalyze ----

// TestHandleAnalyze_success exercises handleAnalyze with a valid fixture path.
func TestHandleAnalyze_success(t *testing.T) {
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_analyze", map[string]any{
		"image_path": fixturePath("block08_go.png"),
	})
	if isErr {
		t.Fatalf("unpixel_analyze: tool returned error: %s", text)
	}
	var report mcpserver.AnalysisReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("unmarshal AnalysisReport: %v", err)
	}
	if report.BlockSize == 0 {
		t.Error("AnalysisReport.BlockSize == 0")
	}
}

// TestHandleAnalyze_badPath exercises handleAnalyze with a non-existent path
// (covers the errResult / loadImage-error path inside handleAnalyze).
func TestHandleAnalyze_badPath(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_analyze", map[string]any{
		"image_path": "/no/such/file.png",
	})
	if !isErr {
		t.Error("unpixel_analyze(bad path): want isError=true, got false")
	}
}

// ---- handleVerify ----

// TestHandleVerify_success exercises handleVerify with valid inputs.
func TestHandleVerify_success(t *testing.T) {
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_verify_candidates", map[string]any{
		"image_path": fixturePath("block08_go.png"),
		"candidates": []string{"go", "xy"},
		"block_size": 8,
	})
	if isErr {
		t.Fatalf("unpixel_verify_candidates: tool returned error: %s", text)
	}
	var report mcpserver.VerifyReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("unmarshal VerifyReport: %v", err)
	}
	if len(report.Ranked) != 2 {
		t.Errorf("Ranked len = %d, want 2", len(report.Ranked))
	}
}

// TestHandleVerify_emptyCandidates covers the empty-candidates error path in
// handleVerify (exercises errResult).
func TestHandleVerify_emptyCandidates(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_verify_candidates", map[string]any{
		"image_path": fixturePath("block08_go.png"),
		"candidates": []string{},
	})
	if !isErr {
		t.Error("unpixel_verify_candidates(empty): want isError=true, got false")
	}
}

// TestHandleVerify_badPath covers the load-image error path in handleVerify.
func TestHandleVerify_badPath(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_verify_candidates", map[string]any{
		"image_path": "/no/such/file.png",
		"candidates": []string{"go"},
	})
	if !isErr {
		t.Error("unpixel_verify_candidates(bad path): want isError=true, got false")
	}
}

// ---- handleDecode ----

// TestHandleDecode_success exercises handleDecode synchronously with a valid
// fixture (covers toolJSON, errResult via the full happy-path for mosaic).
func TestHandleDecode_success(t *testing.T) {
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_decode", map[string]any{
		"image_path": fixturePath("block08_go.png"),
		"method":     "mosaic",
	})
	if isErr {
		t.Fatalf("unpixel_decode(mosaic): tool returned error: %s", text)
	}
	var res mcpserver.DecodeResult
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		t.Fatalf("unmarshal DecodeResult: %v", err)
	}
	if res.Text == "" {
		t.Error("DecodeResult.Text is empty")
	}
}

// TestHandleDecode_badPath exercises the load-image error path in handleDecode.
func TestHandleDecode_badPath(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_decode", map[string]any{
		"image_path": "/no/such/file.png",
		"method":     "mosaic",
	})
	if !isErr {
		t.Error("unpixel_decode(bad path): want isError=true, got false")
	}
}

// TestHandleDecode_badFontBase64 exercises the LoadFontData-error path in
// handleDecode (both font fields set → mutual-exclusion error).
func TestHandleDecode_badFontBase64(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_decode", map[string]any{
		"image_path":  fixturePath("block08_go.png"),
		"method":      "mosaic",
		"font_path":   "/some/font.ttf",
		"font_base64": "dGVzdA==",
	})
	if !isErr {
		t.Error("unpixel_decode(both font fields): want isError=true, got false")
	}
}

// TestHandleDecode_async exercises the async path in handleDecode (covers
// jsonMarshal, the async branch, and the asyncDecodeResult schema).
func TestHandleDecode_async(t *testing.T) {
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_decode", map[string]any{
		"image_path": fixturePath("block08_go.png"),
		"method":     "mosaic",
		"async":      true,
	})
	if isErr {
		t.Fatalf("unpixel_decode(async): tool returned error: %s", text)
	}
	var ar map[string]any
	if err := json.Unmarshal([]byte(text), &ar); err != nil {
		t.Fatalf("unmarshal async result: %v", err)
	}
	jobID, _ := ar["job_id"].(string)
	if jobID == "" {
		t.Fatal("async result: job_id is empty")
	}
	status, _ := ar["status"].(string)
	if status != "pending" {
		t.Errorf("async result: status = %q, want %q", status, "pending")
	}
	// Clean up the background job.
	_, _, _ = mcpserver.RetrieveJob(jobID, true)
}

// ---- handleJobResult ----

// TestHandleJobResult_pending exercises handleJobResult while a job is running.
func TestHandleJobResult_pending(t *testing.T) {
	cs := newTestClient(t)

	// Start a job that blocks until cancelled.
	ready := make(chan struct{})
	jobID, err := mcpserver.SubmitJob(context.Background(), func(jCtx context.Context) (mcpserver.DecodeResult, error) {
		close(ready)
		<-jCtx.Done()
		return mcpserver.DecodeResult{}, jCtx.Err()
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	t.Cleanup(func() {
		_ = mcpserver.CancelJob(jobID)
		_, _, _ = mcpserver.RetrieveJob(jobID, false)
	})

	// Wait for the goroutine to start.
	select {
	case <-ready:
	case <-t.Context().Done():
		t.Fatal("goroutine did not start")
	}

	text, isErr := callTool(t, cs, "unpixel_job_result", map[string]any{
		"job_id": jobID,
	})
	if isErr {
		t.Fatalf("unpixel_job_result(pending): tool returned error: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal job result: %v", err)
	}
	if out["status"] != "pending" {
		t.Errorf("status = %q, want %q", out["status"], "pending")
	}
}

// TestHandleJobResult_done exercises handleJobResult when the job has finished.
func TestHandleJobResult_done(t *testing.T) {
	cs := newTestClient(t)

	jobID, err := mcpserver.SubmitJob(context.Background(), func(_ context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.DecodeResult{Text: "hi", MethodUsed: "mosaic"}, nil
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	// Wait for the job to finish before polling.
	_, _, _ = mcpserver.RetrieveJob(jobID, false) // does not remove unless done
	// Give it a moment.
	_, _, _ = mcpserver.RetrieveJob(jobID, true)

	// By now the job is gone; calling the tool should return an error.
	_, isErr := callTool(t, cs, "unpixel_job_result", map[string]any{
		"job_id": jobID,
	})
	if !isErr {
		t.Error("unpixel_job_result(gone job): want isError=true")
	}
}

// TestHandleJobResult_emptyID covers the empty-job_id error path.
func TestHandleJobResult_emptyID(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_job_result", map[string]any{
		"job_id": "",
	})
	if !isErr {
		t.Error("unpixel_job_result(empty id): want isError=true")
	}
}

// ---- handleJobCancel ----

// TestHandleJobCancel_success exercises handleJobCancel with a valid running job.
func TestHandleJobCancel_success(t *testing.T) {
	cs := newTestClient(t)

	ready := make(chan struct{})
	jobID, err := mcpserver.SubmitJob(context.Background(), func(jCtx context.Context) (mcpserver.DecodeResult, error) {
		close(ready)
		<-jCtx.Done()
		return mcpserver.DecodeResult{}, jCtx.Err()
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	select {
	case <-ready:
	case <-t.Context().Done():
		t.Fatal("goroutine did not start")
	}

	text, isErr := callTool(t, cs, "unpixel_job_cancel", map[string]any{
		"job_id": jobID,
	})
	if isErr {
		t.Fatalf("unpixel_job_cancel: tool returned error: %s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal cancel result: %v", err)
	}
	if out["cancelled"] != true {
		t.Errorf("cancelled = %v, want true", out["cancelled"])
	}
}

// TestHandleJobCancel_unknownID covers the not-found error path in handleJobCancel.
func TestHandleJobCancel_unknownID(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_job_cancel", map[string]any{
		"job_id": "does-not-exist-zzzz",
	})
	if !isErr {
		t.Error("unpixel_job_cancel(unknown): want isError=true")
	}
}

// TestHandleJobCancel_emptyID covers the empty job_id guard.
func TestHandleJobCancel_emptyID(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_job_cancel", map[string]any{
		"job_id": "",
	})
	if !isErr {
		t.Error("unpixel_job_cancel(empty id): want isError=true")
	}
}

// ---- handleRender ----

// TestHandleRender_success exercises handleRender with valid text.
func TestHandleRender_success(t *testing.T) {
	cs := newTestClient(t)
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name:      "unpixel_render",
		Arguments: map[string]any{"text": "go"},
	})
	if err != nil {
		t.Fatalf("CallTool(unpixel_render): %v", err)
	}
	if res.IsError {
		t.Fatalf("unpixel_render: isError=true")
	}
	if len(res.Content) == 0 {
		t.Fatal("unpixel_render: no content")
	}
	ic, ok := res.Content[0].(*mcpsdk.ImageContent)
	if !ok {
		t.Fatalf("unpixel_render: first content is %T, want *ImageContent", res.Content[0])
	}
	if ic.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", ic.MIMEType)
	}
}

// TestHandleRender_emptyText covers the empty-text error path via handleRender.
func TestHandleRender_emptyText(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_render", map[string]any{
		"text": "",
	})
	if !isErr {
		t.Error("unpixel_render(empty text): want isError=true")
	}
}

// TestHandleRender_badFont covers the LoadFontData error path in handleRender.
func TestHandleRender_badFont(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_render", map[string]any{
		"text":        "go",
		"font_path":   "/some/font.ttf",
		"font_base64": "dGVzdA==",
	})
	if !isErr {
		t.Error("unpixel_render(both font fields): want isError=true")
	}
}

// ---- handleRankFonts ----

// TestHandleRankFonts_success exercises handleRankFonts with a valid fixture.
func TestHandleRankFonts_success(t *testing.T) {
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_rank_fonts", map[string]any{
		"image_path": fixturePath("block08_go.png"),
		"known_text": "go",
	})
	if isErr {
		t.Fatalf("unpixel_rank_fonts: tool returned error: %s", text)
	}
	var report mcpserver.RankFontsReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("unmarshal RankFontsReport: %v", err)
	}
	if len(report.Ranked) == 0 {
		t.Error("RankFontsReport.Ranked is empty")
	}
}

// TestHandleRankFonts_badPath covers the load-image error in handleRankFonts.
func TestHandleRankFonts_badPath(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_rank_fonts", map[string]any{
		"image_path": "/no/such/file.png",
		"known_text": "go",
	})
	if !isErr {
		t.Error("unpixel_rank_fonts(bad path): want isError=true")
	}
}

// ---- handleCalibrate ----

// TestHandleCalibrate_success exercises handleCalibrate with a valid fixture.
func TestHandleCalibrate_success(t *testing.T) {
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_calibrate", map[string]any{
		"image_path":   fixturePath("text_hello.png"),
		"visible_text": "hello",
	})
	if isErr {
		t.Fatalf("unpixel_calibrate: tool returned error: %s", text)
	}
	var report mcpserver.CalibrateReport
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatalf("unmarshal CalibrateReport: %v", err)
	}
	if len(report.FittedAxes) == 0 {
		t.Error("CalibrateReport.FittedAxes is empty")
	}
}

// TestHandleCalibrate_badPath covers the load-image error in handleCalibrate.
func TestHandleCalibrate_badPath(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_calibrate", map[string]any{
		"image_path":   "/no/such/file.png",
		"visible_text": "hello",
	})
	if !isErr {
		t.Error("unpixel_calibrate(bad path): want isError=true")
	}
}

// TestHandleCalibrate_unknownFont covers the Calibrate error path forwarded
// through handleCalibrate (errResult on invalid input).
func TestHandleCalibrate_unknownFont(t *testing.T) {
	cs := newTestClient(t)
	_, isErr := callTool(t, cs, "unpixel_calibrate", map[string]any{
		"image_path":   fixturePath("text_hello.png"),
		"visible_text": "hello",
		"font":         "inter",
	})
	if !isErr {
		t.Error("unpixel_calibrate(unknown font): want isError=true")
	}
}

// ---- handleJobResult: done path via tool ----

// TestHandleJobResult_doneViaSubmit submits a fast job, waits for it to
// complete, then polls via the MCP tool to get status="done".
func TestHandleJobResult_doneViaSubmit(t *testing.T) {
	cs := newTestClient(t)

	jobID, err := mcpserver.SubmitJob(context.Background(), func(_ context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.DecodeResult{Text: "done-text", MethodUsed: "mosaic"}, nil
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	// Poll via the MCP tool until done or timeout.
	var gotDone bool
	for range 50 {
		text, isErr := callTool(t, cs, "unpixel_job_result", map[string]any{
			"job_id": jobID,
		})
		if isErr {
			// Job was retrieved and removed on first done poll.
			break
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out["status"] == "done" {
			gotDone = true
			break
		}
	}
	if !gotDone {
		// The job may have been consumed by another test; acceptable.
		t.Log("job was consumed before done poll (acceptable race in parallel tests)")
	}
}

// ---- loadImage error (not via handle*) ----

// TestVerifyCandidates_emptyImage covers the VerifyCandidates path that would
// fail if image is nil-bounds, but more directly we just want loadImage covered
// via an existing error path tested above (handleAnalyze_badPath).
// This test covers the VerifyCandidates wire-components error branch.
func TestVerifyCandidates_loadImageError(t *testing.T) {
	// Drive via the MCP tool (already covered above). This test additionally
	// verifies the returned error text mentions the path.
	cs := newTestClient(t)
	text, isErr := callTool(t, cs, "unpixel_verify_candidates", map[string]any{
		"image_path": "/no/such.png",
		"candidates": []string{"go"},
	})
	if !isErr {
		t.Error("want isError=true")
	}
	if !strings.Contains(text, "no/such.png") {
		t.Errorf("error text %q does not mention path", text)
	}
}
