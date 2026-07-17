package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOutboxWritesOnlyVersionOne(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	ev, err := newSkillEvent("codex", "session-1", "", "", "brainstorming", time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ev); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if string(envelope["schema_version"]) != "1" {
		t.Fatalf("schema_version = %s, want 1", envelope["schema_version"])
	}
	if _, legacy := envelope["skill"]; legacy {
		t.Fatal("version 1 outbox entry must not contain legacy skill field")
	}
}

func TestOutboxReadsMixedLegacyAndVersionOneInOrder(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	legacy := `{"agent":"codex","session_id":"legacy-1","skill":"old","ts":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(s.Dir, "0001.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	ev, err := newSkillEvent("codex", "versioned-1", "", "", "new", time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "0002.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, name := range names {
		read, err := s.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, read.Payload.(SkillPayload).SkillName)
	}
	if len(got) != 2 || got[0] != "old" || got[1] != "new" {
		t.Fatalf("skills = %v, want [old new]", got)
	}
}

func TestOutboxKeepsInvalidVersionedEntry(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	const invalid = `{"schema_version":1,"event_name":"skill_executed","agent":"codex","session_id":"s1","ts":"2026-01-01T00:00:00Z","payload":{"skill_name":""}}`
	name := "0001.json"
	if err := os.WriteFile(filepath.Join(s.Dir, name), []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(name); err == nil {
		t.Fatal("want strict decoder to reject invalid versioned entry")
	}
	names, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != name {
		t.Fatalf("buffer = %v, want [%s]", names, name)
	}
}

func testSkillEvent(t *testing.T, agent, session, remote, repoDir, skill string, ts time.Time) TelemetryEvent {
	t.Helper()
	ev, err := newSkillEvent(agent, session, normalizeRawRemote(remote), repoDir, skill, ts)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func skillName(t *testing.T, ev TelemetryEvent) string {
	t.Helper()
	payload, ok := ev.Payload.(SkillPayload)
	if !ok {
		t.Fatalf("payload = %T, want SkillPayload", ev.Payload)
	}
	return payload.SkillName
}

func TestTelemetryEventJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	in := testSkillEvent(t, "codex", "s1", "git@host:org/repo.git", "", "ops:deploy", ts)
	b, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out TelemetryEvent
	if err := out.UnmarshalJSON(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Agent != in.Agent || out.SessionID != in.SessionID || out.RepoRemote != in.RepoRemote ||
		!out.TS.Equal(in.TS) || skillName(t, out) != skillName(t, in) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestOutboxEnqueueAndList(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}

	ev := testSkillEvent(t, "codex", "s1", "", "", "a", time.Unix(1, 0).UTC())
	if err := s.Enqueue(ev); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	files, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}

	got, err := s.Read(files[0])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if skillName(t, got) != "a" {
		t.Fatalf("read skill = %q", skillName(t, got))
	}

	if err := s.Remove(files[0]); err != nil {
		t.Fatalf("remove: %v", err)
	}
	files, _ = s.List()
	if len(files) != 0 {
		t.Fatalf("after remove got %d files, want 0", len(files))
	}
}

func TestOutboxListIgnoresTmpAndMarker(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}
	if err := os.WriteFile(filepath.Join(dir, "x.tmp"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, flushStampName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := s.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("got %d files, want 0", len(files))
	}
}

func TestOutboxRotateDropsOldest(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}
	for i := 0; i < 5; i++ {
		ev := testSkillEvent(t, "codex", "s1", "", "", "s", time.Unix(int64(i)+1, 0).UTC())
		if err := s.Enqueue(ev); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond) // ensure distinct nanos in filenames
	}
	dropped, err := s.Rotate(3)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	files, _ := s.List()
	if len(files) != 3 {
		t.Fatalf("remaining = %d, want 3", len(files))
	}
}

func TestOutboxRotateUnderCapNoop(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}
	_ = s.Enqueue(testSkillEvent(t, "codex", "s1", "", "", "s", time.Unix(1, 0).UTC()))
	dropped, err := s.Rotate(100)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if dropped != 0 {
		t.Fatalf("dropped = %d, want 0", dropped)
	}
}

func TestOffsetStoreRoundTrip(t *testing.T) {
	o := &OffsetStore{Dir: t.TempDir()}
	if got := o.Load("codex:s1"); got != 0 {
		t.Fatalf("fresh offset = %d, want 0", got)
	}
	if err := o.Save("codex:s1", 4096); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := o.Load("codex:s1"); got != 4096 {
		t.Fatalf("offset = %d, want 4096", got)
	}
}

func TestOffsetStoreKeysAreIsolated(t *testing.T) {
	o := &OffsetStore{Dir: t.TempDir()}
	_ = o.Save("codex:s1", 10)
	_ = o.Save("codex:s2", 20)
	if o.Load("codex:s1") != 10 || o.Load("codex:s2") != 20 {
		t.Fatal("keys collided")
	}
}

func TestOffsetStoreOverwrite(t *testing.T) {
	o := &OffsetStore{Dir: t.TempDir()}
	_ = o.Save("k", 5)
	_ = o.Save("k", 9)
	if got := o.Load("k"); got != 9 {
		t.Fatalf("offset = %d, want 9", got)
	}
}
