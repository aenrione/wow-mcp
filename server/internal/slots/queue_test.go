// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition.

package slots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testQueue(t *testing.T, dir string, at *time.Time) *Queue {
	t.Helper()
	return &Queue{File: filepath.Join(dir, "slots.json"), Now: func() time.Time { return *at }}
}

func addOnsDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Interface", "AddOns")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRequestIdsAreMonotonicAndBelow2to31(t *testing.T) {
	at := time.Unix(1_764_000_000, 0)
	q := testQueue(t, t.TempDir(), &at)
	var last int64
	for i := 0; i < 100; i++ {
		id, err := q.Request("waypoint", map[string]any{"x": 1.0})
		if err != nil {
			t.Fatal(err)
		}
		if id <= last {
			t.Fatalf("id %d after %d: not monotonic", id, last)
		}
		if id >= 1<<31 {
			t.Fatalf("id %d does not fit a 32-bit Lua integer", id)
		}
		last = id
	}
}

func TestAckRemovesAndIsIdempotent(t *testing.T) {
	at := time.Unix(1_764_000_000, 0)
	q := testQueue(t, t.TempDir(), &at)
	a, _ := q.Request("waypoint", nil)
	b, _ := q.Request("focus", nil)
	if err := q.Ack([]int64{a}); err != nil {
		t.Fatal(err)
	}
	pending, _ := q.Pending()
	if len(pending) != 1 || requestID(pending[0]) != b {
		t.Fatalf("pending after ack: %v", pending)
	}
	// Re-acking an already answered id and acking an unknown one change nothing.
	if err := q.Ack([]int64{a, 999}); err != nil {
		t.Fatal(err)
	}
	if pending, _ = q.Pending(); len(pending) != 1 {
		t.Fatalf("pending after re-ack: %v", pending)
	}
}

// A restart must continue the id sequence and keep the pending requests.
func TestPersistenceAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	at := time.Unix(1_764_000_000, 0)
	q1 := testQueue(t, dir, &at)
	id1, _ := q1.Request("waypoint", map[string]any{"map": 1426})

	// A later restart whose clock-derived seed would be LOWER than lastId (clock went back).
	earlier := time.Unix(1_000_000, 0)
	q2 := testQueue(t, dir, &earlier)
	pending, err := q2.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || requestID(pending[0]) != id1 {
		t.Fatalf("pending not reloaded: %v", pending)
	}
	id2, _ := q2.Request("focus", nil)
	if id2 <= id1 {
		t.Fatalf("restart reused ids: %d after %d", id2, id1)
	}
}

func TestFlushCoalesces(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 2); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_764_000_000, 0)
	q := testQueue(t, t.TempDir(), &at)

	wrote, err := q.Flush(addons, false)
	if err != nil || !wrote {
		t.Fatalf("first flush must write: %v %v", wrote, err)
	}
	if wrote, _ = q.Flush(addons, false); wrote {
		t.Fatal("nothing changed within 30 s: the flush must coalesce")
	}
	if _, err := q.Request("waypoint", map[string]any{"map": 1426}); err != nil {
		t.Fatal(err)
	}
	if wrote, _ = q.Flush(addons, false); !wrote {
		t.Fatal("pending changed: the flush must write")
	}
	at = at.Add(31 * time.Second)
	if wrote, _ = q.Flush(addons, false); !wrote {
		t.Fatal("30 s passed: the heartbeat must refresh the timestamp")
	}
	if wrote, _ = q.Flush(addons, true); !wrote {
		t.Fatal("force must always write")
	}
}

func TestWriteAllRewritesEverySlotBody(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 3); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_764_000_000, 0)
	q := testQueue(t, t.TempDir(), &at)
	id, _ := q.Request("waypoint", map[string]any{"map": 1426, "x": 52.6, "y": 53.2})
	if err := q.WriteAll(addons); err != nil {
		t.Fatal(err)
	}
	want := "WowMcpInbox = " + Literal(map[string]any{
		"t": at.Unix(),
		"reqs": []any{map[string]any{
			"id": id, "kind": "waypoint", "map": 1426, "x": 52.6, "y": 53.2,
		}},
		"got": []any{},
	}) + "\n"
	for n := 1; n <= 3; n++ {
		body, err := os.ReadFile(filepath.Join(addons, SlotName(n), "body.lua"))
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != want {
			t.Errorf("slot %d body:\n%s\nwant:\n%s", n, body, want)
		}
	}
	// Atomic writes leave no temporary files behind.
	for n := 1; n <= 3; n++ {
		entries, _ := os.ReadDir(filepath.Join(addons, SlotName(n)))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				t.Errorf("slot %d: leftover temp file %s", n, e.Name())
			}
		}
	}
}

func TestWriteAllTouchesOnlySlotFolders(t *testing.T) {
	addons := addOnsDir(t)
	if err := Install(addons, 1); err != nil {
		t.Fatal(err)
	}
	stranger := filepath.Join(addons, "Questie")
	if err := os.MkdirAll(stranger, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stranger, "body.lua"), []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	fiveDigits := filepath.Join(addons, "WowMcp_S00001")
	if err := os.MkdirAll(fiveDigits, 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_764_000_000, 0)
	q := testQueue(t, t.TempDir(), &at)
	if err := q.WriteAll(addons); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(stranger, "body.lua"))
	if string(data) != "untouched" {
		t.Errorf("another addon's body.lua was rewritten: %s", data)
	}
	if _, err := os.Stat(filepath.Join(fiveDigits, "body.lua")); !os.IsNotExist(err) {
		t.Error("a folder outside ^WowMcp_S\\d{4}$ was written")
	}
}

func TestWriteAllRefusesWithoutSlots(t *testing.T) {
	at := time.Unix(1_764_000_000, 0)
	q := testQueue(t, t.TempDir(), &at)
	if err := q.WriteAll(addOnsDir(t)); err == nil || !strings.Contains(err.Error(), "no request slots") {
		t.Fatalf("an empty AddOns folder must be reported: %v", err)
	}
}
