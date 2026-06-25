package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSkillEventJSONRoundTrip(t *testing.T) {
	ts := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	in := SkillEvent{
		Agent:      "codex",
		SessionID:  "s1",
		RepoRemote: "git@host:org/repo.git",
		Skill:      "ops:deploy",
		TS:         ts,
	}
	b, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SkillEvent
	if err := out.UnmarshalJSON(b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", out, in)
	}
}

func TestOutboxEnqueueAndList(t *testing.T) {
	dir := t.TempDir()
	s := &Outbox{Dir: dir}

	ev := SkillEvent{Agent: "codex", Skill: "a", TS: time.Unix(1, 0).UTC()}
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
	if got.Skill != "a" {
		t.Fatalf("read skill = %q", got.Skill)
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
		if err := s.Enqueue(SkillEvent{Skill: "s", TS: time.Unix(int64(i), 0).UTC()}); err != nil {
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
	_ = s.Enqueue(SkillEvent{Skill: "s", TS: time.Unix(1, 0).UTC()})
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
