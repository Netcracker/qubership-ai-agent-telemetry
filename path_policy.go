package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const pathAllowFileName = "path-allow"

type pathFlavor uint8

const (
	pathPOSIX pathFlavor = iota
	pathWindows
)

type policyPath struct {
	flavor   pathFlavor
	volume   string
	segments []string
}

type pathPattern struct {
	all bool
	policyPath
}

func validatePathAllow(patterns []string) error {
	for _, pattern := range patterns {
		if _, err := parsePathPattern(pattern); err != nil {
			return err
		}
	}
	return nil
}

func loadPathAllowFile(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read path policy: %w", err)
	}
	patterns := make([]string, 0)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := validatePathAllow(patterns); err != nil {
		return nil, err
	}
	return patterns, nil
}

func pathAllowed(paths, allow []string) bool {
	if len(paths) == 0 || len(allow) == 0 || validatePathAllow(allow) != nil {
		return false
	}
	for _, candidate := range uniquePolicyPaths(paths) {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		canonical, err := filepath.EvalSymlinks(filepath.Clean(absolute))
		if err != nil {
			continue
		}
		for _, pattern := range allow {
			matched, err := pathPatternMatch(pattern, canonical)
			if err == nil && matched {
				return true
			}
		}
	}
	return false
}

func uniquePolicyPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func parsePathPattern(pattern string) (pathPattern, error) {
	if strings.IndexFunc(pattern, unicode.IsControl) >= 0 {
		return pathPattern{}, fmt.Errorf("path pattern contains a control character")
	}
	if pattern == "*" {
		return pathPattern{all: true}, nil
	}
	if pattern == "" {
		return pathPattern{}, fmt.Errorf("path pattern must not be empty")
	}
	if strings.HasPrefix(pattern, "!") {
		return pathPattern{}, fmt.Errorf("deny path patterns are not supported: %q", pattern)
	}
	parsed, err := parsePolicyPath(pattern, true)
	if err != nil {
		return pathPattern{}, fmt.Errorf("invalid path pattern %q: %w", pattern, err)
	}
	if strings.HasPrefix(parsed.volume, "//") {
		for _, segment := range strings.Split(strings.TrimPrefix(parsed.volume, "//"), "/") {
			if segment == "." || segment == ".." || strings.ContainsAny(segment, "*?[]") {
				return pathPattern{}, fmt.Errorf("invalid path pattern %q: UNC server and share must be literal", pattern)
			}
		}
	}
	for _, segment := range parsed.segments {
		if segment == "." || segment == ".." {
			return pathPattern{}, fmt.Errorf("invalid path pattern %q: dot segments are not supported", pattern)
		}
		if strings.ContainsAny(segment, "?[]") {
			return pathPattern{}, fmt.Errorf("invalid path pattern %q: unsupported wildcard", pattern)
		}
		if strings.Contains(segment, "**") && segment != "**" {
			return pathPattern{}, fmt.Errorf("invalid path pattern %q: ** must be a complete segment", pattern)
		}
	}
	return pathPattern{policyPath: parsed}, nil
}

func pathPatternMatch(pattern, candidate string) (bool, error) {
	compiled, err := parsePathPattern(pattern)
	if err != nil {
		return false, err
	}
	if candidate == "" {
		return false, nil
	}
	path, err := parsePolicyPath(candidate, false)
	if err != nil {
		return false, nil
	}
	if compiled.all {
		return true, nil
	}
	if compiled.flavor != path.flavor || !pathTextEqual(compiled.flavor, compiled.volume, path.volume) {
		return false, nil
	}
	return matchPolicyPathSegments(compiled.segments, path.segments, compiled.flavor), nil
}

func parsePolicyPath(value string, pattern bool) (policyPath, error) {
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return policyPath{}, fmt.Errorf("path must not be empty")
	}
	if isUNCPath(value) {
		normalized := strings.ReplaceAll(value, `\`, "/")
		parts := nonemptyPathSegments(strings.TrimLeft(normalized, "/"))
		if len(parts) < 2 {
			return policyPath{}, fmt.Errorf("UNC path must include a server and share")
		}
		segments, err := normalizePolicySegments(parts[2:], pattern)
		if err != nil {
			return policyPath{}, err
		}
		return policyPath{flavor: pathWindows, volume: "//" + parts[0] + "/" + parts[1], segments: segments}, nil
	}
	if isWindowsDrivePath(value) {
		normalized := strings.ReplaceAll(value[3:], `\`, "/")
		segments, err := normalizePolicySegments(nonemptyPathSegments(normalized), pattern)
		if err != nil {
			return policyPath{}, err
		}
		return policyPath{flavor: pathWindows, volume: strings.ToUpper(value[:2]), segments: segments}, nil
	}
	if !strings.HasPrefix(value, "/") {
		return policyPath{}, fmt.Errorf("path must be absolute")
	}
	segments, err := normalizePolicySegments(nonemptyPathSegments(strings.TrimLeft(value, "/")), pattern)
	if err != nil {
		return policyPath{}, err
	}
	return policyPath{flavor: pathPOSIX, volume: "/", segments: segments}, nil
}

func isUNCPath(value string) bool {
	return len(value) >= 2 && (value[0] == '/' || value[0] == '\\') && (value[1] == '/' || value[1] == '\\')
}

func isWindowsDrivePath(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func nonemptyPathSegments(value string) []string {
	parts := strings.Split(value, "/")
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normalizePolicySegments(parts []string, pattern bool) ([]string, error) {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case ".":
			if pattern {
				return nil, fmt.Errorf("dot segments are not supported")
			}
		case "..":
			if pattern {
				return nil, fmt.Errorf("dot segments are not supported")
			}
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return out, nil
}

func matchPolicyPathSegments(pattern, candidate []string, flavor pathFlavor) bool {
	if len(pattern) == 0 {
		return len(candidate) == 0
	}
	if pattern[0] == "**" {
		if matchPolicyPathSegments(pattern[1:], candidate, flavor) {
			return true
		}
		return len(candidate) > 0 && matchPolicyPathSegments(pattern, candidate[1:], flavor)
	}
	return len(candidate) > 0 && pathSegmentMatch(pattern[0], candidate[0], flavor) &&
		matchPolicyPathSegments(pattern[1:], candidate[1:], flavor)
}

func pathSegmentMatch(pattern, candidate string, flavor pathFlavor) bool {
	if flavor == pathWindows {
		pattern = strings.ToLower(pattern)
		candidate = strings.ToLower(candidate)
	}
	for len(pattern) > 0 {
		if pattern[0] != '*' {
			if len(candidate) == 0 || pattern[0] != candidate[0] {
				return false
			}
			pattern = pattern[1:]
			candidate = candidate[1:]
			continue
		}
		pattern = strings.TrimLeft(pattern, "*")
		if pattern == "" {
			return true
		}
		for i := 0; i <= len(candidate); i++ {
			if pathSegmentMatch(pattern, candidate[i:], flavor) {
				return true
			}
		}
		return false
	}
	return candidate == ""
}

func pathTextEqual(flavor pathFlavor, left, right string) bool {
	if flavor == pathWindows {
		return strings.EqualFold(left, right)
	}
	return left == right
}
