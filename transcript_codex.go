package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
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
func codexTranscriptEvents(stdin []byte, offsets *OffsetStore, now time.Time) []TelemetryEvent {
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
	defer func() { _ = f.Close() }()

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

	events := make([]TelemetryEvent, 0, len(scan.skills))
	for _, name := range scan.skills {
		ev, err := newSkillEvent(
			"codex", p.SessionID, scan.repoRemote, firstNonEmpty(scan.repoDir, p.Cwd), name, now,
		)
		if err == nil {
			events = append(events, ev)
		}
	}
	return events
}

// codexTranscriptEventsAuto wires codexTranscriptEvents to the default offset
// store. It skips building the store unless the payload actually names a
// transcript, so the marker-only path touches no extra state.
func codexTranscriptEventsAuto(stdin []byte, now time.Time) []TelemetryEvent {
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
	repoDir    string   // session_meta.cwd, kept local for repo-scope policy checks
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
			Cwd string `json:"cwd"`
			Git struct {
				RepositoryURL string `json:"repository_url"`
			} `json:"git"`
		}
		if json.Unmarshal(env.Payload, &m) == nil {
			out.repoDir = m.Cwd
			if m.Git.RepositoryURL != "" {
				out.repoRemote = sanitizeRemote(m.Git.RepositoryURL)
			}
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func codexToolTexts(typ, name, arguments, input string) []string {
	switch {
	case typ == "custom_tool_call" && name == "exec":
		return codexCustomCommandTexts(input)
	case typ == "function_call":
		return codexFunctionCallTexts(name, arguments)
	default:
		return nil
	}
}

func codexCustomCommandTexts(input string) []string {
	markers := []string{"tools.exec_command", "tools.shell_command"}
	var texts []string
	for i := 0; i < len(input); {
		if input[i] == '\'' || input[i] == '"' || input[i] == '`' {
			i = skipJSString(input, i)
			continue
		}
		if next, ok := skipJSComment(input, i); ok {
			i = next
			continue
		}

		marker := ""
		for _, candidate := range markers {
			if validJSToolBoundary(input, i) && strings.HasPrefix(input[i:], candidate) {
				marker = candidate
				break
			}
		}
		if marker == "" {
			i++
			continue
		}

		open := i + len(marker)
		for open < len(input) && isJSSpace(input[open]) {
			open++
		}
		if open >= len(input) || input[open] != '(' {
			i += len(marker)
			continue
		}
		close, ok := matchingJSParen(input, open)
		if !ok {
			break
		}
		if command, ok := jsCommandArgument(input[open+1 : close]); ok {
			texts = append(texts, command)
		}
		i = close + 1
	}
	return texts
}

func matchingJSParen(input string, open int) (int, bool) {
	depth := 1
	for i := open + 1; i < len(input); {
		if next, ok := skipJSComment(input, i); ok {
			i = next
			continue
		}
		switch input[i] {
		case '\'', '"', '`':
			i = skipJSString(input, i)
		case '(':
			depth++
			i++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
			i++
		default:
			i++
		}
	}
	return 0, false
}

func jsCommandArgument(args string) (string, bool) {
	i := skipJSTrivia(args, 0)
	if i >= len(args) || args[i] != '{' {
		return "", false
	}
	i++
	for i < len(args) {
		i = skipJSTrivia(args, i)
		if i < len(args) && args[i] == ',' {
			i = skipJSTrivia(args, i+1)
		}
		if i >= len(args) || args[i] == '}' {
			return "", false
		}

		var key string
		if args[i] == '\'' || args[i] == '"' || args[i] == '`' {
			var ok bool
			key, i, ok = readJSString(args, i)
			if !ok {
				return "", false
			}
		} else {
			start := i
			for i < len(args) && (isJSIdentifier(args[i]) || args[i] == '-') {
				i++
			}
			key = args[start:i]
		}
		i = skipJSTrivia(args, i)
		if i >= len(args) || args[i] != ':' {
			return "", false
		}
		i++
		i = skipJSTrivia(args, i)
		if key == "cmd" || key == "command" {
			if i >= len(args) || (args[i] != '\'' && args[i] != '"' && args[i] != '`') {
				return "", false
			}
			value, _, ok := readJSString(args, i)
			return value, ok
		}

		i = skipJSValue(args, i)
	}
	return "", false
}

func skipJSValue(input string, start int) int {
	braces, brackets, parens := 0, 0, 0
	for i := start; i < len(input); {
		if next, ok := skipJSComment(input, i); ok {
			i = next
			continue
		}
		switch input[i] {
		case '\'', '"', '`':
			i = skipJSString(input, i)
		case '{':
			braces++
			i++
		case '}':
			if braces == 0 && brackets == 0 && parens == 0 {
				return i
			}
			braces--
			i++
		case '[':
			brackets++
			i++
		case ']':
			brackets--
			i++
		case '(':
			parens++
			i++
		case ')':
			parens--
			i++
		case ',':
			if braces == 0 && brackets == 0 && parens == 0 {
				return i + 1
			}
			i++
		default:
			i++
		}
	}
	return len(input)
}

func skipJSString(input string, start int) int {
	quote := input[start]
	for i := start + 1; i < len(input); i++ {
		if input[i] == '\\' {
			i++
			continue
		}
		if input[i] == quote {
			return i + 1
		}
	}
	return len(input)
}

func skipJSComment(input string, start int) (int, bool) {
	if strings.HasPrefix(input[start:], "//") {
		if end := strings.IndexByte(input[start+2:], '\n'); end >= 0 {
			return start + end + 3, true
		}
		return len(input), true
	}
	if strings.HasPrefix(input[start:], "/*") {
		if end := strings.Index(input[start+2:], "*/"); end >= 0 {
			return start + end + 4, true
		}
		return len(input), true
	}
	return start, false
}

func skipJSTrivia(input string, start int) int {
	for start < len(input) {
		if isJSSpace(input[start]) {
			start++
			continue
		}
		if next, ok := skipJSComment(input, start); ok {
			start = next
			continue
		}
		break
	}
	return start
}

func readJSString(input string, start int) (string, int, bool) {
	end := skipJSString(input, start)
	if end > len(input) || end == len(input) && input[end-1] != input[start] {
		return "", end, false
	}
	literal := input[start:end]
	if input[start] == '"' {
		value, err := strconv.Unquote(literal)
		return value, end, err == nil
	}
	body := literal[1 : len(literal)-1]
	value := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' || i+1 >= len(body) {
			value = append(value, body[i])
			continue
		}
		i++
		switch body[i] {
		case 'n':
			value = append(value, '\n')
		case 'r':
			value = append(value, '\r')
		case 't':
			value = append(value, '\t')
		default:
			value = append(value, body[i])
		}
	}
	return string(value), end, true
}

func isJSSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func isJSIdentifier(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$'
}

func validJSToolBoundary(input string, start int) bool {
	return start == 0 || !isJSIdentifier(input[start-1]) && input[start-1] != '.'
}

func codexFunctionCallTexts(name, arguments string) []string {
	if arguments == "" {
		return nil
	}
	if name != "exec_command" && name != "shell_command" {
		return nil
	}
	var args map[string]any
	if json.Unmarshal([]byte(arguments), &args) != nil {
		return nil
	}

	var texts []string
	for _, key := range []string{"cmd", "command"} {
		if s, ok := args[key].(string); ok && s != "" {
			texts = append(texts, s)
		}
	}
	return texts
}
