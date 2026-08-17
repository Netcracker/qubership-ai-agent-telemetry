package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const legacyDefaultRepoAllow = "github.com/Netcracker/*"

type repoScopeChange string

const (
	repoScopeChangeAsk    repoScopeChange = ""
	repoScopeChangeAccept repoScopeChange = "accept"
	repoScopeChangeKeep   repoScopeChange = "keep"
)

type repoScopeUpdateService struct {
	Prepare func(lifecycleOptions) error
	Apply   func() (operationResult, bool)
}

func newRepoScopeUpdateService(configDir func() string, envValue func() string, input io.Reader, output io.Writer) repoScopeUpdateService {
	if configDir == nil {
		configDir = pkgConfigDir
	}
	if envValue == nil {
		envValue = func() string { return "" }
	}
	if input == nil {
		input = strings.NewReader("")
	}
	if output == nil {
		output = io.Discard
	}

	var prepared bool
	var expand bool
	var candidatePath string
	var candidateBytes []byte
	var candidateInfo os.FileInfo
	return repoScopeUpdateService{
		Prepare: func(opts lifecycleOptions) error {
			prepared = false
			expand = false
			candidatePath = ""
			candidateBytes = nil
			candidateInfo = nil
			if opts.Action != actionUpdate || len(splitList(envValue())) > 0 {
				return nil
			}
			dir := strings.TrimSpace(configDir())
			if dir == "" {
				return nil
			}
			path := repoAllowPath(dir)
			data, info, ok := canonicalLegacyRepoAllowSnapshot(path)
			if !ok {
				return nil
			}
			prepared = true
			candidatePath = path
			candidateBytes = data
			candidateInfo = info
			switch opts.RepoScopeChange {
			case repoScopeChangeAccept:
				expand = true
			case repoScopeChangeKeep:
				return nil
			case repoScopeChangeAsk:
				if opts.NonInteractive {
					return nil
				}
				accepted, err := confirmRepoScopeExpansion(input, output)
				if err != nil {
					return err
				}
				expand = accepted
			default:
				return fmt.Errorf("unknown repository scope change %q", opts.RepoScopeChange)
			}
			return nil
		},
		Apply: func() (operationResult, bool) {
			if !prepared {
				return operationResult{}, false
			}
			if !expand {
				return operationResult{Name: "repo-policy", State: operationSkipped, Detail: "existing scope preserved"}, true
			}
			data, info, exact := canonicalLegacyRepoAllowSnapshot(candidatePath)
			if len(splitList(envValue())) > 0 || !exact || !os.SameFile(candidateInfo, info) || !bytes.Equal(candidateBytes, data) {
				return operationResult{Name: "repo-policy", State: operationSkipped, Detail: "scope changed during update; preserved"}, true
			}
			if err := writeRepoAllowFile(filepath.Dir(candidatePath), defaultRepoAllow); err != nil {
				return operationResult{Name: "repo-policy", State: operationFailed, Detail: "cannot expand repository scope", Err: err}, true
			}
			return operationResult{Name: "repo-policy", State: operationOK, Detail: "repository scope expanded"}, true
		},
	}
}

func canonicalLegacyRepoAllowSnapshot(path string) ([]byte, os.FileInfo, bool) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil, nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, false
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, nil, false
	}
	want := []byte(legacyDefaultRepoAllow + "\n")
	if !bytes.Equal(data, want) {
		return nil, nil, false
	}
	return append([]byte(nil), data...), after, true
}

func repoAllowPath(configDir string) string {
	return filepath.Join(configDir, repoAllowFileName)
}

func confirmRepoScopeExpansion(input io.Reader, output io.Writer) (bool, error) {
	_, _ = fmt.Fprintf(output,
		"The repository scope is %s. Add %s to collect repositories from Netcracker-related hosts? "+
			"The pattern can also match an unrelated host containing 'netcracker'. [y/N] ",
		legacyDefaultRepoAllow, "*netcracker*/**")
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read repository scope confirmation: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
