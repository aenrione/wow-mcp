// SPDX-License-Identifier: GPL-3.0-or-later
// Forever Suite fork addition (port of wow-seer's request slots).

package slots

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// heartbeat is how often the slot bodies are rewritten when nothing changed: the addon
// trusts an inbox only when |time() - inbox.t| < 180 s, so the timestamp must stay fresh.
const heartbeat = 30 * time.Second

// maxID keeps request ids below 2^31: Forever's Lua integers are 32-bit.
const maxID = int64(1) << 31

// Queue is the pending-request list behind the slot channel. Request ids are monotonic and
// survive restarts through a small JSON file in the server's own directory (never in the
// game directory), so a restarted server cannot reuse an id the addon already acknowledged.
type Queue struct {
	// File is the persistence file (pending + lastId), e.g. slots.json next to goals.json.
	File string
	// Now is the clock; nil means time.Now (tests inject their own).
	Now func() time.Time

	mu        sync.Mutex
	loaded    bool
	lastID    int64
	pending   []map[string]any
	lastWrite time.Time
	lastKey   string
}

// queueFile is the JSON shape of the persistence file.
type queueFile struct {
	LastID  int64            `json:"lastId"`
	Pending []map[string]any `json:"pending"`
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

// load reads the persistence file once; a missing file seeds lastID from the clock and pid
// (the wow-seer formula), which keeps ids monotonic across restarts even without the file.
func (q *Queue) load() error {
	if q.loaded {
		return nil
	}
	q.loaded = true
	q.lastID = (q.now().Unix()%10_000_000)*100 + int64(os.Getpid()%10)
	data, err := os.ReadFile(q.File)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var file queueFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("slot queue file %s: %w", q.File, err)
	}
	if file.LastID > q.lastID {
		q.lastID = file.LastID
	}
	q.pending = file.Pending
	return nil
}

// persist writes pending + lastId atomically to the queue file.
func (q *Queue) persist() error {
	data, err := json.Marshal(queueFile{LastID: q.lastID, Pending: q.pending})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(q.File), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(q.File, data)
}

// Request queues a request of the given kind (the fields are flattened next to id and kind,
// the shape the addon expects) and returns its id. The queue is persisted before returning.
func (q *Queue) Request(kind string, fields map[string]any) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.load(); err != nil {
		return 0, err
	}
	q.lastID += 10
	if q.lastID >= maxID {
		q.lastID = q.lastID%maxID + 100
	}
	req := map[string]any{"id": q.lastID, "kind": kind}
	for k, v := range fields {
		if k != "id" && k != "kind" {
			req[k] = v
		}
	}
	q.pending = append(q.pending, req)
	if err := q.persist(); err != nil {
		return 0, err
	}
	return q.lastID, nil
}

// Ack removes the requests with the given ids from the pending list (the addon answered
// them); unknown ids are ignored, so re-acking is harmless. The queue is persisted.
func (q *Queue) Ack(ids []int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.load(); err != nil {
		return err
	}
	done := map[int64]bool{}
	for _, id := range ids {
		done[id] = true
	}
	kept := q.pending[:0]
	for _, req := range q.pending {
		if !done[requestID(req)] {
			kept = append(kept, req)
		}
	}
	q.pending = kept
	return q.persist()
}

// requestID reads the id of a pending request; ids reloaded from JSON arrive as float64.
func requestID(req map[string]any) int64 {
	switch id := req["id"].(type) {
	case int64:
		return id
	case float64:
		return int64(id)
	default:
		return 0
	}
}

// Pending returns a copy of the pending requests.
func (q *Queue) Pending() ([]map[string]any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.load(); err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(q.pending))
	copy(out, q.pending)
	return out, nil
}

// LastWrite is when the slot bodies were last rewritten (zero before the first write).
func (q *Queue) LastWrite() time.Time {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.lastWrite
}

// WriteAll rewrites the body of EVERY installed slot with the current inbox, atomically
// (temp file + rename, with the Windows busy-file retry). All slots, not only the next ones:
// /reload rewinds the addon to slot 1, so every body must carry the same inbox.
func (q *Queue) WriteAll(addOnsDir string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.load(); err != nil {
		return err
	}
	return q.writeAllLocked(addOnsDir)
}

func (q *Queue) writeAllLocked(addOnsDir string) error {
	names, err := SlotDirs(addOnsDir)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no request slots (WowMcp_S0001...) in %s: install them first (install script --slots, or slots.Install)", addOnsDir)
	}
	body := []byte("WowMcpInbox = " + Literal(q.inboxLocked()) + "\n")
	for _, name := range names {
		if err := writeFileAtomic(filepath.Join(addOnsDir, name, "body.lua"), body); err != nil {
			return err
		}
	}
	q.lastWrite = q.now()
	q.lastKey = q.keyLocked()
	return nil
}

// inboxLocked is the value every slot body assigns to WowMcpInbox.
func (q *Queue) inboxLocked() map[string]any {
	reqs := make([]any, len(q.pending))
	for i, req := range q.pending {
		reqs[i] = req
	}
	return map[string]any{"t": q.now().Unix(), "reqs": reqs, "got": []any{}}
}

// keyLocked fingerprints the pending list, so Flush can tell whether anything changed.
func (q *Queue) keyLocked() string {
	data, _ := json.Marshal(q.pending)
	return string(data)
}

// Flush rewrites the slot bodies when forced, when the pending list changed since the last
// write, or when the heartbeat (30 s) passed — otherwise it coalesces and does nothing.
// It returns whether it wrote. Callers flush with force=true right after queueing a request.
func (q *Queue) Flush(addOnsDir string, force bool) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.load(); err != nil {
		return false, err
	}
	fresh := q.now().Sub(q.lastWrite) < heartbeat
	if !force && fresh && q.keyLocked() == q.lastKey {
		return false, nil
	}
	if err := q.writeAllLocked(addOnsDir); err != nil {
		return false, err
	}
	return true, nil
}

// Engine binds a Queue to one AddOns directory: the shape the MCP tools consume
// (the SlotChannel port in mcpserver/tools_remote.go).
type Engine struct {
	Queue     *Queue
	AddOnsDir string
}

// Request queues a request and returns its id.
func (e Engine) Request(kind string, fields map[string]any) (int64, error) {
	return e.Queue.Request(kind, fields)
}

// Flush writes the slot bodies (see Queue.Flush) and says whether it wrote.
func (e Engine) Flush(force bool) (bool, error) { return e.Queue.Flush(e.AddOnsDir, force) }

// Pending is the number of queued, unanswered requests.
func (e Engine) Pending() (int, error) {
	pending, err := e.Queue.Pending()
	return len(pending), err
}

// LastWrite is when the slot bodies were last rewritten.
func (e Engine) LastWrite() time.Time { return e.Queue.LastWrite() }

// Installed counts the installed slot folders.
func (e Engine) Installed() (int, error) {
	names, err := SlotDirs(e.AddOnsDir)
	return len(names), err
}
