package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SkillEvent is the normalized, agent-independent record produced by an adapter
// and persisted in the outbox. It is the only shape that leaves this process.
type SkillEvent struct {
	Agent      string    `json:"agent"`
	SessionID  string    `json:"session_id"`
	RepoRemote string    `json:"repo_remote,omitempty"`
	Skill      string    `json:"skill"`
	TS         time.Time `json:"ts"`
}

func (e SkillEvent) MarshalJSON() ([]byte, error) {
	type alias SkillEvent
	return json.Marshal(alias(e))
}

func (e *SkillEvent) UnmarshalJSON(b []byte) error {
	type alias SkillEvent
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	*e = SkillEvent(a)
	return nil
}

const flushStampName = ".lastflush"

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
func (s *Outbox) Enqueue(ev SkillEvent) error {
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
func (s *Outbox) Read(name string) (SkillEvent, error) {
	var ev SkillEvent
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
