package mcpserver_test

import (
	"encoding/json"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestResources_fonts verifies that the fonts resource returns a non-empty
// JSON array of font entries.
func TestResources_fonts(t *testing.T) {
	payload, err := mcpserver.ResourcePayload("unpixel://fonts")
	if err != nil {
		t.Fatalf("ResourcePayload(fonts): %v", err)
	}
	if payload == "" {
		t.Fatal("fonts resource: empty payload")
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		t.Fatalf("fonts resource: invalid JSON: %v", err)
	}
	if len(entries) == 0 {
		t.Error("fonts resource: no entries")
	}
	// Every entry must have a non-empty name.
	for i, e := range entries {
		if e["name"] == "" || e["name"] == nil {
			t.Errorf("fonts resource entry %d: missing name", i)
		}
	}
}

// TestResources_charsets verifies that the charsets resource returns the
// expected preset entries (lower, alnum, ascii, digits) with non-empty rune strings.
func TestResources_charsets(t *testing.T) {
	payload, err := mcpserver.ResourcePayload("unpixel://charsets")
	if err != nil {
		t.Fatalf("ResourcePayload(charsets): %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		t.Fatalf("charsets resource: invalid JSON: %v", err)
	}
	// Expect lower, alnum, ascii, digits.
	if len(entries) != 4 {
		t.Errorf("charsets resource: got %d entries, want 4", len(entries))
	}
	for i, e := range entries {
		if e["runes"] == "" || e["runes"] == nil {
			t.Errorf("charsets entry %d: missing runes", i)
		}
	}
	// Verify the "digits" preset is present.
	found := false
	for _, e := range entries {
		if e["preset"] == "digits" {
			found = true
			break
		}
	}
	if !found {
		t.Error("charsets resource: missing 'digits' preset")
	}
}

// TestResources_methods verifies that the methods resource returns at least
// 10 method entries, each with non-empty method and use_when fields.
func TestResources_methods(t *testing.T) {
	payload, err := mcpserver.ResourcePayload("unpixel://methods")
	if err != nil {
		t.Fatalf("ResourcePayload(methods): %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		t.Fatalf("methods resource: invalid JSON: %v", err)
	}
	if len(entries) < 10 {
		t.Errorf("methods resource: got %d entries, want >= 10", len(entries))
	}
	for i, e := range entries {
		if e["method"] == "" || e["method"] == nil {
			t.Errorf("methods entry %d: missing method", i)
		}
		if e["use_when"] == "" || e["use_when"] == nil {
			t.Errorf("methods entry %d: missing use_when", i)
		}
	}
}

// TestResources_operatingEnvelope verifies that the operating-envelope resource
// returns a non-empty JSON array.
func TestResources_operatingEnvelope(t *testing.T) {
	payload, err := mcpserver.ResourcePayload("unpixel://operating-envelope")
	if err != nil {
		t.Fatalf("ResourcePayload(operating-envelope): %v", err)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		t.Fatalf("operating-envelope resource: invalid JSON: %v", err)
	}
	if len(entries) == 0 {
		t.Error("operating-envelope resource: no entries")
	}
}

// TestResources_unknownURI verifies that an unknown URI returns an error.
func TestResources_unknownURI(t *testing.T) {
	_, err := mcpserver.ResourcePayload("unpixel://nonexistent")
	if err == nil {
		t.Error("ResourcePayload(nonexistent): want error, got nil")
	}
}
