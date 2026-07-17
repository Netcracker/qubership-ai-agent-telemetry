package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	flushStampName        = ".lastflush"
	lastDeliveryErrorName = ".last_delivery_error"
)

// Outbox is a machine-global directory holding one JSON file per buffered event.
type Outbox struct {
	Dir string
}

// cacheBase resolves the root under which the outbox and offset store live. Like
// configBase, it is a uniform XDG-style path on every OS — $XDG_CACHE_HOME, else
// ~/.cache — so a packaged harness (Claude Desktop on Windows, whose %LocalAppData%
// is virtualized by MSIX) and a plain shell share one cache. Returns "" when
// neither a cache dir nor a home dir is available.
func cacheBase() string {
	return cacheBaseFrom(os.Getenv("XDG_CACHE_HOME"), userHomeDir())
}

// cacheBaseFrom is the testable core: an explicit $XDG_CACHE_HOME wins, else fall
// back to <home>/.cache. Empty when both inputs are empty.
func cacheBaseFrom(xdg, home string) string {
	return xdgBaseFrom(xdg, home, ".cache")
}

// DefaultOutbox returns the per-machine outbox rooted in the user cache dir.
func DefaultOutbox() (*Outbox, error) {
	base := cacheBase()
	if base == "" {
		return nil, fmt.Errorf("no cache directory available")
	}
	dir := filepath.Join(base, pkgName, "outbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Outbox{Dir: dir}, nil
}

// Enqueue writes one event atomically (temp file + rename).
func (s *Outbox) Enqueue(ev TelemetryEvent) error {
	ev.EventID = newUUID()
	if ev.EventID == "" {
		return fmt.Errorf("generate event ID: secure random source unavailable")
	}
	if err := validateSerializableEvent(ev); err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%d-%s.json", time.Now().UnixNano(), os.Getpid(), randHex())
	final := filepath.Join(s.Dir, name)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// List returns event file names (not paths), oldest first, excluding temp files
// and the flush stamp.
func (s *Outbox) List() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".tmp") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names) // filenames start with zero-padded nanos => chronological
	return names, nil
}

// Read decodes one event file by name.
func (s *Outbox) Read(name string) (TelemetryEvent, error) {
	var ev TelemetryEvent
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return ev, err
	}
	err = json.Unmarshal(b, &ev)
	return ev, err
}

// Remove deletes one event file by name.
func (s *Outbox) Remove(name string) error {
	return os.Remove(filepath.Join(s.Dir, name))
}

func lastDeliveryErrorPath(s *Outbox) string {
	return filepath.Join(s.Dir, lastDeliveryErrorName)
}

func recordLastDeliveryError(s *Outbox, err error) {
	if err == nil {
		return
	}
	msg := strings.ReplaceAll(err.Error(), "\n", " ")
	_ = os.WriteFile(lastDeliveryErrorPath(s), []byte(msg), 0o600)
}

func readLastDeliveryError(s *Outbox) (string, bool) {
	b, err := os.ReadFile(lastDeliveryErrorPath(s))
	if err != nil {
		return "", false
	}
	msg := strings.TrimSpace(string(b))
	return msg, msg != ""
}

func clearLastDeliveryError(s *Outbox) {
	_ = os.Remove(lastDeliveryErrorPath(s))
}

// Rotate deletes the oldest event files until at most limit remain.
// Returns how many were dropped. (limit avoids shadowing the builtin cap/max.)
func (s *Outbox) Rotate(limit int) (int, error) {
	names, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(names) <= limit {
		return 0, nil
	}
	drop := names[:len(names)-limit] // List is oldest-first
	for _, n := range drop {
		if err := s.Remove(n); err != nil {
			return 0, err
		}
	}
	return len(drop), nil
}

func randHex() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// eventIDForDelivery returns a validated persisted ID. Legacy or malformed
// records receive a deterministic UUID derived only from their opaque outbox
// file name, so retries stay stable without transmitting untrusted content.
func eventIDForDelivery(ev TelemetryEvent, name string) string {
	if validUUIDv4(ev.EventID) {
		return ev.EventID
	}
	sum := sha256.Sum256([]byte(name))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// OffsetStore persists a per-session byte offset into a harness transcript, so
// each Stop run ingests only the lines written since the previous run. It is
// harness-agnostic: callers namespace the key (for example "codex:<session>")
// to keep different harnesses from colliding. Named for the byte offset it
// holds, not the Cursor harness.
type OffsetStore struct {
	Dir string
}

// DefaultOffsetStore roots the offset directory in the user cache dir, beside
// the outbox. It returns an error when no cache dir is available.
func DefaultOffsetStore() (*OffsetStore, error) {
	base := cacheBase()
	if base == "" {
		return nil, fmt.Errorf("no cache directory available")
	}
	dir := filepath.Join(base, pkgName, "offsets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &OffsetStore{Dir: dir}, nil
}

// path maps a key to a safe file name, regardless of the key's shape.
func (o *OffsetStore) path(key string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_", ".", "_").Replace(key)
	return filepath.Join(o.Dir, safe+".offset")
}

// lock serializes a complete load-process-save transaction for one transcript
// session across hook processes. Atomic Save protects the offset file itself,
// but cannot prevent two processes from reading and processing the same offset.
func (o *OffsetStore) lock(key string) (release func(), err error) {
	fl := flock.New(o.path(key) + ".lock")
	if err := fl.Lock(); err != nil {
		return nil, err
	}
	return func() { _ = fl.Unlock() }, nil
}

// Load returns the stored byte offset for key, or 0 when none is recorded or
// the stored value is unreadable.
func (o *OffsetStore) Load(key string) int64 {
	b, err := os.ReadFile(o.path(key))
	if err != nil {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Save records the byte offset for key with an atomic temp-file rename.
func (o *OffsetStore) Save(key string, off int64) error {
	final := o.path(key)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.FormatInt(off, 10)), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}
