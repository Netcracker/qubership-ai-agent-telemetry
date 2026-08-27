package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func harnessFlagValues(includeAll bool) []string {
	values := make([]string, 0, len(allHookTargets)+1)
	for _, target := range allHookTargets {
		values = append(values, string(target))
	}
	if includeAll {
		values = append([]string{"all"}, values...)
	}
	return values
}

func hookFlagValues(includeSelectors bool) []string {
	values := harnessFlagValues(false)
	if includeSelectors {
		values = append([]string{"all", "none"}, values...)
	}
	return values
}

func enumValuesDescription(values []string) string {
	return strings.Join(values, ", ")
}

func normalizeHarnesses(values []string) ([]hookTarget, error) {
	if len(values) == 0 {
		return append([]hookTarget(nil), allHookTargets...), nil
	}
	selected := make(map[hookTarget]bool)
	all := false
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return nil, fmt.Errorf("harness name must not be empty")
		}
		if value == "all" {
			all = true
			continue
		}
		target := hookTarget(value)
		if !knownHookTarget(target) {
			return nil, fmt.Errorf("unknown harness %q; valid harnesses: %s", raw, enumValuesDescription(harnessFlagValues(true)))
		}
		selected[target] = true
	}
	if all && len(selected) != 0 {
		return nil, fmt.Errorf("harness %q must be used alone", "all")
	}
	if all {
		return append([]hookTarget(nil), allHookTargets...), nil
	}
	targets := make([]hookTarget, 0, len(selected))
	for _, target := range allHookTargets {
		if selected[target] {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("harness selection must not be empty")
	}
	return targets, nil
}

func normalizeLifecycleOptions(opts lifecycleOptions) (lifecycleOptions, error) {
	switch opts.Action {
	case actionInstall, actionUpdate, actionUninstall:
	default:
		return lifecycleOptions{}, fmt.Errorf("unknown lifecycle action %q", opts.Action)
	}

	if opts.Purge && opts.Action != actionUninstall {
		return lifecycleOptions{}, fmt.Errorf("--purge is valid only for uninstall")
	}
	if opts.Action == actionUninstall {
		if opts.Harnesses != nil {
			return lifecycleOptions{}, fmt.Errorf("harness selection is not valid for uninstall")
		}
		if opts.NonInteractive {
			return lifecycleOptions{}, fmt.Errorf("--non-interactive is not valid for uninstall")
		}
		return opts, nil
	}

	if opts.Harnesses != nil && len(opts.Harnesses) == 0 {
		return lifecycleOptions{}, fmt.Errorf("harness selection must not be empty")
	}
	rawHarnesses := make([]string, len(opts.Harnesses))
	for i, target := range opts.Harnesses {
		rawHarnesses[i] = string(target)
	}
	var err error
	opts.Harnesses, err = normalizeHarnesses(rawHarnesses)
	if err != nil {
		return lifecycleOptions{}, err
	}
	return opts, nil
}

func completeCSV(allowed []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	baseDirective := cobra.ShellCompDirectiveNoFileComp
	lastComma := strings.LastIndex(toComplete, ",")
	prefix := ""
	partial := toComplete
	committed := []string(nil)
	if lastComma >= 0 {
		prefix = toComplete[:lastComma+1]
		partial = toComplete[lastComma+1:]
		committed = strings.Split(toComplete[:lastComma], ",")
	}

	allowedSet := make(map[string]bool, len(allowed))
	regularAllowed := make(map[string]bool, len(allowed))
	for _, value := range allowed {
		allowedSet[value] = true
		if !exclusiveCSVSelector(value) {
			regularAllowed[value] = true
		}
	}
	selected := make(map[string]bool, len(committed))
	for _, raw := range committed {
		value := strings.TrimSpace(raw)
		if value == "" || !allowedSet[value] || exclusiveCSVSelector(value) || selected[value] {
			return []string{}, baseDirective
		}
		selected[value] = true
	}

	trimmedPartial := strings.TrimSpace(partial)
	candidates := make([]string, 0, len(allowed))
	canContinue := false
	for _, value := range allowed {
		if exclusiveCSVSelector(value) && len(committed) != 0 {
			continue
		}
		if selected[value] || !strings.HasPrefix(value, trimmedPartial) {
			continue
		}
		candidates = append(candidates, prefix+value)
		if !exclusiveCSVSelector(value) {
			remaining := 0
			for candidate := range regularAllowed {
				if candidate != value && !selected[candidate] {
					remaining++
				}
			}
			canContinue = canContinue || remaining > 0
		}
	}
	if len(candidates) != 0 && canContinue {
		return candidates, baseDirective | cobra.ShellCompDirectiveNoSpace
	}
	return candidates, baseDirective
}

func exclusiveCSVSelector(value string) bool {
	return value == "all" || value == "none"
}
