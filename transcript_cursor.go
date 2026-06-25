package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// cursorManualSkillRe matches a `Skill Name: <name>` line inside the
// <manually_attached_skills> block Cursor inlines on a manual /skill-name call.
var cursorManualSkillRe = regexp.MustCompile(`(?m)^Skill Name:\s*(\S+)`)

// scanCursorTranscript streams a Cursor transcript and returns the skill names
// read at or beyond startOffset, in order and deduped, plus the end-of-file byte
// offset to persist as the next offset. Unlike Codex, the repo remote is not in
// the transcript, so it is resolved from the hook payload instead.
func scanCursorTranscript(r io.Reader, startOffset int64) ([]string, int64) {
	var skills []string
	seen := map[string]bool{}
	br := bufio.NewReader(r)
	var pos int64
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			lineStart := pos
			pos += int64(len(line))
			if lineStart >= startOffset {
				processCursorLine(line, &skills, seen)
			}
		}
		if err != nil {
			break
		}
	}
	return skills, pos
}

func processCursorLine(line string, skills *[]string, seen map[string]bool) {
	var env struct {
		Message struct {
			Content []struct {
				Type  string `json:"type"`
				Name  string `json:"name"`
				Text  string `json:"text"`
				Input struct {
					Path string `json:"path"`
				} `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &env) != nil {
		return
	}
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			*skills = append(*skills, name)
		}
	}
	for _, c := range env.Message.Content {
		switch c.Type {
		case "tool_use":
			if c.Name != "Read" {
				continue
			}
			if name, ok := skillNameInPath(c.Input.Path); ok {
				add(name)
			}
		case "text":
			// Block-presence-gated, not block-bounded: once the manual-attach
			// block is present, the regex scans the whole text, so a stray
			// "Skill Name:" line elsewhere in the same message would also match.
			// The cost is a spurious name, never a failure, and the block is the
			// only realistic source of that line.
			if !strings.Contains(c.Text, "<manually_attached_skills>") {
				continue
			}
			for _, m := range cursorManualSkillRe.FindAllStringSubmatch(c.Text, -1) {
				add(m[1])
			}
		}
	}
}

// cursorTranscriptEvents reads the transcript named by transcript_path and
// returns one event per skill read since the last run. It never fails the
// caller: any problem yields zero events. When offsets is non-nil and the
// payload carries a session id, only reads beyond the stored byte offset are
// emitted, and the offset advances to the end of the file. The remote is
// resolved from workspace_roots, since the transcript carries no git data.
func cursorTranscriptEvents(stdin []byte, offsets *OffsetStore, remote remoteResolver, now time.Time) []SkillEvent {
	var p cursorPayload
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &p)
	}
	if p.TranscriptPath == "" {
		return nil
	}
	f, err := os.Open(p.TranscriptPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var offset int64
	key := "cursor:" + p.SessionID
	useOffset := offsets != nil && p.SessionID != ""
	if useOffset {
		offset = offsets.Load(key)
		if fi, serr := f.Stat(); serr == nil && offset > fi.Size() {
			offset = 0 // file rotated or truncated since the last run
		}
	}

	skills, end := scanCursorTranscript(f, offset)

	if useOffset {
		_ = offsets.Save(key, end)
	}

	rem := cursorRemote(p, remote)
	events := make([]SkillEvent, 0, len(skills))
	for _, name := range skills {
		events = append(events, SkillEvent{
			Agent:      "cursor",
			SessionID:  p.SessionID,
			RepoRemote: rem,
			Skill:      name,
			TS:         now,
		})
	}
	return events
}

// cursorTranscriptEventsAuto wires cursorTranscriptEvents to the default offset
// store. It skips building the store unless the payload names a transcript.
func cursorTranscriptEventsAuto(stdin []byte, remote remoteResolver, now time.Time) []SkillEvent {
	var p cursorPayload
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &p)
	}
	if p.TranscriptPath == "" {
		return nil
	}
	store, err := DefaultOffsetStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "offset:", err)
		store = nil
	}
	return cursorTranscriptEvents(stdin, store, remote, now)
}
