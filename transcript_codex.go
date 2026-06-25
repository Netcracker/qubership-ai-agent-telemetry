package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// codexTranscriptEvents reads the rollout named by transcript_path in the Stop
// payload and returns one event per skill SKILL.md read since the last run. It
// never fails the caller: any problem yields zero events, never an error.
//
// When offsets is non-nil and the payload carries a session id, only reads
// beyond the stored byte offset are emitted, and the offset advances to the end
// of the file. session_meta is always read for the repo remote, since it sits
// on the first line, before any offset.
func codexTranscriptEvents(stdin []byte, offsets *OffsetStore, now time.Time) []SkillEvent {
	var p codexPayload
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
	key := "codex:" + p.SessionID
	useOffset := offsets != nil && p.SessionID != ""
	if useOffset {
		offset = offsets.Load(key)
		if fi, serr := f.Stat(); serr == nil && offset > fi.Size() {
			offset = 0 // file rotated or truncated since the last run
		}
	}

	scan, end := scanCodexRollout(f, offset)

	if useOffset {
		_ = offsets.Save(key, end)
	}

	events := make([]SkillEvent, 0, len(scan.skills))
	for _, name := range scan.skills {
		events = append(events, SkillEvent{
			Agent:      "codex",
			SessionID:  p.SessionID,
			RepoRemote: scan.repoRemote,
			Skill:      name,
			TS:         now,
		})
	}
	return events
}

// codexTranscriptEventsAuto wires codexTranscriptEvents to the default offset
// store. It skips building the store unless the payload actually names a
// transcript, so the marker-only path touches no extra state.
func codexTranscriptEventsAuto(stdin []byte, now time.Time) []SkillEvent {
	var p codexPayload
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
	return codexTranscriptEvents(stdin, store, now)
}

type codexScan struct {
	skills     []string // skill names read at or beyond the offset, in order, deduped
	repoRemote string   // session_meta.git.repository_url, read across the whole file
}

// scanCodexRollout streams a Codex rollout. It always reads session_meta for the
// repo remote, but emits a skill read only when its line begins at or after
// startOffset. It returns the scan and the end-of-file byte offset, to persist
// as the next offset.
func scanCodexRollout(r io.Reader, startOffset int64) (codexScan, int64) {
	var out codexScan
	seen := map[string]bool{}
	br := bufio.NewReader(r)
	var pos int64
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			lineStart := pos
			pos += int64(len(line))
			processCodexLine(line, lineStart >= startOffset, &out, seen)
		}
		if err != nil {
			break
		}
	}
	return out, pos
}

func processCodexLine(line string, emit bool, out *codexScan, seen map[string]bool) {
	var env struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	if json.Unmarshal([]byte(line), &env) != nil {
		return
	}
	switch env.Type {
	case "session_meta":
		var m struct {
			Git struct {
				RepositoryURL string `json:"repository_url"`
			} `json:"git"`
		}
		if json.Unmarshal(env.Payload, &m) == nil && m.Git.RepositoryURL != "" {
			out.repoRemote = sanitizeRemote(m.Git.RepositoryURL)
		}
	case "response_item":
		if !emit {
			return
		}
		var fc struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Input     string `json:"input"`
		}
		if json.Unmarshal(env.Payload, &fc) != nil {
			return
		}
		texts := codexToolTexts(fc.Type, fc.Name, fc.Arguments, fc.Input)
		if len(texts) == 0 {
			return
		}
		for _, text := range texts {
			for _, name := range skillNamesInText(text) {
				if !seen[name] {
					seen[name] = true
					out.skills = append(out.skills, name)
				}
			}
		}
	}
}

func codexToolTexts(typ, name, arguments, input string) []string {
	switch {
	case typ == "custom_tool_call" && name == "exec":
		if input == "" {
			return nil
		}
		return []string{input}
	case typ == "function_call":
		return codexFunctionCallTexts(name, arguments)
	default:
		return nil
	}
}

func codexFunctionCallTexts(name, arguments string) []string {
	if arguments == "" {
		return nil
	}
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return nil
	}

	var keys []string
	if name == "exec_command" || name == "shell_command" {
		keys = []string{"cmd", "command"}
	}

	var texts []string
	for _, key := range keys {
		if s, ok := args[key].(string); ok && s != "" {
			texts = append(texts, s)
		}
	}
	if len(texts) > 0 {
		return texts
	}

	for _, v := range args {
		if s, ok := v.(string); ok && s != "" && skillPathRe.MatchString(s) {
			texts = append(texts, s)
		}
	}
	return texts
}
