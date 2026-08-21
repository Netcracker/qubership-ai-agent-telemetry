package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// cursorManualSkillRe matches a `Skill Name: <name>` line inside the
// <manually_attached_skills> block Cursor inlines on a manual /skill-name call.
var cursorManualSkillRe = regexp.MustCompile(`(?m)^Skill Name:\s*(\S+)`)

var cursorPathInputKeys = []string{
	"path", "file_path", "target_file", "target_directory", "working_directory", "cwd",
}

const maxCursorEvidencePaths = 256

type cursorTranscriptScan struct {
	Skills         []string
	Paths          []string
	PathsTruncated bool
	End            int64
}

func scanCursorTranscriptEvidence(r io.Reader, startOffset int64) cursorTranscriptScan {
	var result cursorTranscriptScan
	skillsSeen := map[string]bool{}
	pathsSeen := map[string]bool{}
	br := bufio.NewReader(r)
	var pos int64
	for {
		line, err := br.ReadString('\n')
		if err == io.EOF && len(line) > 0 && !strings.HasSuffix(line, "\n") {
			break // wait for Cursor to finish the final JSONL record
		}
		if len(line) > 0 {
			lineStart := pos
			pos += int64(len(line))
			if lineStart >= startOffset {
				processCursorEvidenceLine(line, &result, skillsSeen, pathsSeen)
			}
		}
		if err != nil {
			break
		}
	}
	result.End = pos
	return result
}

func processCursorEvidenceLine(line string, result *cursorTranscriptScan, skillsSeen, pathsSeen map[string]bool) {
	var env struct {
		Message struct {
			Content []struct {
				Type  string                     `json:"type"`
				Name  string                     `json:"name"`
				Text  string                     `json:"text"`
				Input map[string]json.RawMessage `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &env) != nil {
		return
	}
	addSkill := func(name string) {
		if name != "" && !skillsSeen[name] {
			skillsSeen[name] = true
			result.Skills = append(result.Skills, name)
		}
	}
	addPath := func(candidate string) {
		if candidate == "" || pathsSeen[candidate] {
			return
		}
		if len(result.Paths) >= maxCursorEvidencePaths {
			result.PathsTruncated = true
			return
		}
		pathsSeen[candidate] = true
		result.Paths = append(result.Paths, candidate)
	}
	for _, c := range env.Message.Content {
		switch c.Type {
		case "tool_use":
			if path, ok := cursorInputString(c.Input, "path"); ok {
				if name, isSkill := skillNameInPath(path); isSkill {
					if cursorSkillReadTool(c.Name) {
						addSkill(name)
					}
				} else {
					addPath(path)
				}
			}
			for _, key := range cursorPathInputKeys[1:] {
				if path, ok := cursorInputString(c.Input, key); ok {
					if _, isSkill := skillNameInPath(path); !isSkill {
						addPath(path)
					}
				}
			}
		case "text":
			if !strings.Contains(c.Text, "<manually_attached_skills>") {
				continue
			}
			for _, m := range cursorManualSkillRe.FindAllStringSubmatch(c.Text, -1) {
				addSkill(m[1])
			}
		}
	}
}

func cursorSkillReadTool(name string) bool {
	return name == "Read" || name == "ReadFile"
}

func cursorInputString(input map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := input[key]
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return "", false
	}
	return value, true
}

func cursorEvidenceRepo(paths, workspaceRoots []string, remote remoteResolver) string {
	roots := make([]string, 0, len(workspaceRoots))
	for _, root := range workspaceRoots {
		if root != "" {
			roots = append(roots, filepath.Clean(root))
		}
	}
	repositories := make([]string, 0)
	seenRepositories := map[string]bool{}
	rootCache := map[string]string{}
	for _, candidate := range paths {
		path, ok := cursorEvidencePath(candidate, roots)
		if !ok {
			continue
		}
		dir := nearestExistingDirectory(path)
		if dir == "" {
			continue
		}
		root, cached := rootCache[dir]
		if !cached {
			root = cursorGitRoot(dir)
			rootCache[dir] = root
		}
		if root == "" || !cursorPathWithinAnyRoot(root, roots) {
			continue
		}
		if !seenRepositories[root] {
			seenRepositories[root] = true
			repositories = append(repositories, root)
		}
	}
	if len(repositories) == 1 {
		return repositories[0]
	}
	if len(repositories) < 2 {
		return ""
	}

	commonRemote := ""
	for _, root := range repositories {
		normalized := normalizeRawRemote(resolveRemote(remote, root))
		if normalized == "" {
			return ""
		}
		if commonRemote == "" {
			commonRemote = normalized
		} else if normalized != commonRemote {
			return ""
		}
	}
	return repositories[0]
}

func cursorEvidencePath(candidate string, workspaceRoots []string) (string, bool) {
	if candidate == "" {
		return "", false
	}
	var path string
	if filepath.IsAbs(candidate) {
		path = filepath.Clean(candidate)
	} else if len(workspaceRoots) == 1 {
		path = filepath.Join(workspaceRoots[0], candidate)
	} else {
		return "", false
	}
	return path, cursorPathWithinAnyRoot(path, workspaceRoots)
}

func cursorPathWithinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		relative, err := filepath.Rel(root, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func nearestExistingDirectory(path string) string {
	for path != "" {
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return path
			}
			return filepath.Dir(path)
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
	return ""
}

func cursorGitRoot(directory string) string {
	output, err := exec.Command("git", "-C", directory, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(string(output)))
}

// cursorTranscriptEvents reads the transcript named by transcript_path and
// returns one event per skill read since the last run. It never fails the
// caller: any problem yields zero events. When offsets is non-nil and the
// payload carries a session id, only reads beyond the stored byte offset are
// emitted, and the offset advances to the end of the file. The remote is
// resolved from workspace_roots, since the transcript carries no git data.
func cursorTranscriptEvents(stdin []byte, offsets *OffsetStore, remote remoteResolver, now time.Time) []TelemetryEvent {
	var p cursorPayload
	if len(stdin) > 0 {
		_ = json.Unmarshal(stdin, &p)
	}
	if p.TranscriptPath == "" {
		return nil
	}
	events := cursorTranscriptEventsForPath(p, offsets, remote, now, p.AgentTranscriptPath == "")
	for _, transcriptPath := range cursorDirectSubagentTranscriptPaths(p.TranscriptPath) {
		child := p
		child.TranscriptPath = transcriptPath
		events = append(events, cursorTranscriptEventsForPath(child, offsets, remote, now, false)...)
	}
	return events
}

func cursorTranscriptEventsForPath(
	p cursorPayload, offsets *OffsetStore, remote remoteResolver, now time.Time, migrateLegacyParentOffset bool,
) []TelemetryEvent {
	f, err := os.Open(p.TranscriptPath)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	var offset int64
	key := cursorTranscriptOffsetKey(p.SessionID, p.TranscriptPath)
	useOffset := offsets != nil && p.SessionID != ""
	if useOffset {
		release, lockErr := offsets.lock(key)
		if lockErr != nil {
			return nil
		}
		defer release()

		if migrateLegacyParentOffset && !offsets.exists(key) {
			offset = offsets.Load("cursor:" + p.SessionID)
		} else {
			offset = offsets.Load(key)
		}
		if fi, serr := f.Stat(); serr == nil && offset > fi.Size() {
			offset = 0 // file rotated or truncated since the last run
		}
	}

	scan := scanCursorTranscriptEvidence(f, offset)

	if useOffset {
		_ = offsets.Save(key, scan.End)
	}

	repoDir := ""
	if !scan.PathsTruncated {
		repoDir = cursorEvidenceRepo(scan.Paths, p.WorkspaceRoots, remote)
	}
	rem := resolveRemote(remote, repoDir)
	events := make([]TelemetryEvent, 0, len(scan.Skills))
	for _, name := range scan.Skills {
		ev, err := newSkillEvent("cursor", p.SessionID, rem, repoDir, name, now)
		if err == nil {
			events = append(events, ev)
		}
	}
	return events
}

func cursorDirectSubagentTranscriptPaths(parentTranscriptPath string) []string {
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(parentTranscriptPath), "subagents", "*.jsonl"))
	if err != nil {
		return nil
	}
	return paths
}

func cursorTranscriptOffsetKey(sessionID, transcriptPath string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(transcriptPath)))
	return fmt.Sprintf("cursor:%s:%x", sessionID, sum)
}

// cursorTranscriptEventsAuto wires cursorTranscriptEvents to the default offset
// store. It skips building the store unless the payload names a transcript.
func cursorTranscriptEventsAuto(stdin []byte, remote remoteResolver, now time.Time) []TelemetryEvent {
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
