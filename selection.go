package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var lifecycleComponentOrder = []componentName{componentAPM, componentTelemetry, componentGitHooks}

func componentFlagValues(includeAll bool) []string {
	values := make([]string, 0, len(lifecycleComponentOrder)+1)
	for _, component := range lifecycleComponentOrder {
		values = append(values, string(component))
	}
	if includeAll {
		values = append([]string{"all"}, values...)
	}
	return values
}

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

func allComponents() []componentName {
	return append([]componentName(nil), lifecycleComponentOrder...)
}

func normalizeSelection(selected, skipped []string) ([]componentName, error) {
	selectedSet, err := parseComponentSet(selected)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		selectedSet = componentSet(lifecycleComponentOrder)
	}

	skippedSet, err := parseComponentSet(skipped)
	if err != nil {
		return nil, err
	}
	for component := range skippedSet {
		delete(selectedSet, component)
	}

	components := orderedComponents(selectedSet)
	if len(components) == 0 {
		return nil, fmt.Errorf("component selection must not be empty")
	}
	return components, nil
}

func parseComponentSet(values []string) (map[componentName]bool, error) {
	set := make(map[componentName]bool)
	all := false
	for _, raw := range values {
		value := strings.ToLower(strings.TrimSpace(raw))
		if value == "" {
			return nil, fmt.Errorf("component name must not be empty")
		}
		if value == "all" {
			all = true
			continue
		}
		component := componentName(value)
		if !knownComponent(component) {
			return nil, fmt.Errorf("unknown component %q; valid components: %s", raw, enumValuesDescription(componentFlagValues(true)))
		}
		set[component] = true
	}
	if all && len(set) != 0 {
		return nil, fmt.Errorf("component %q must be used alone", "all")
	}
	if all {
		return componentSet(lifecycleComponentOrder), nil
	}
	return set, nil
}

func componentSet(components []componentName) map[componentName]bool {
	set := make(map[componentName]bool, len(components))
	for _, component := range components {
		set[component] = true
	}
	return set
}

func orderedComponents(set map[componentName]bool) []componentName {
	components := make([]componentName, 0, len(set))
	for _, component := range lifecycleComponentOrder {
		if set[component] {
			components = append(components, component)
		}
	}
	return components
}

func knownComponent(component componentName) bool {
	for _, known := range lifecycleComponentOrder {
		if component == known {
			return true
		}
	}
	return false
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
	if opts.Action != actionUpdate && opts.RepoScopeChange != repoScopeChangeAsk {
		return lifecycleOptions{}, fmt.Errorf("--repo-scope-change is valid only for update")
	}
	switch opts.RepoScopeChange {
	case repoScopeChangeAsk, repoScopeChangeAccept, repoScopeChangeKeep:
	default:
		return lifecycleOptions{}, fmt.Errorf("invalid --repo-scope-change value %q; valid values: accept, keep", opts.RepoScopeChange)
	}

	if opts.CLIOnly {
		if opts.Action != actionUpdate {
			return lifecycleOptions{}, fmt.Errorf("--cli-only is valid only for update")
		}
		if opts.Components != nil {
			return lifecycleOptions{}, fmt.Errorf("--cli-only cannot be combined with component options")
		}
		if opts.Harnesses != nil {
			return lifecycleOptions{}, fmt.Errorf("--cli-only cannot be combined with harness options")
		}
		if opts.ForceGitHooks {
			return lifecycleOptions{}, fmt.Errorf("--cli-only cannot be combined with --force-git-hooks")
		}
		if opts.NonInteractive {
			return lifecycleOptions{}, fmt.Errorf("--cli-only cannot be combined with --non-interactive")
		}
		if opts.Purge || opts.RemoveCLI {
			return lifecycleOptions{}, fmt.Errorf("--cli-only cannot be combined with uninstall options")
		}
		return opts, nil
	}

	if opts.Purge && opts.Action != actionUninstall {
		return lifecycleOptions{}, fmt.Errorf("--purge is valid only for uninstall")
	}
	if opts.RemoveCLI && opts.Action != actionUninstall {
		return lifecycleOptions{}, fmt.Errorf("--remove-cli is valid only for uninstall")
	}
	if opts.Action == actionUninstall {
		if opts.Harnesses != nil {
			return lifecycleOptions{}, fmt.Errorf("harness selection is not valid for uninstall")
		}
		if opts.ForceGitHooks {
			return lifecycleOptions{}, fmt.Errorf("--force-git-hooks is not valid for uninstall")
		}
		if opts.NonInteractive {
			return lifecycleOptions{}, fmt.Errorf("--non-interactive is not valid for uninstall")
		}
	}

	if opts.Components != nil && len(opts.Components) == 0 {
		return lifecycleOptions{}, fmt.Errorf("component selection must not be empty")
	}
	rawComponents := make([]string, len(opts.Components))
	for i, component := range opts.Components {
		rawComponents[i] = string(component)
	}
	components, err := normalizeSelection(rawComponents, nil)
	if err != nil {
		return lifecycleOptions{}, err
	}
	opts.Components = components

	if opts.Action != actionUninstall {
		if opts.Harnesses != nil && len(opts.Harnesses) == 0 {
			return lifecycleOptions{}, fmt.Errorf("harness selection must not be empty")
		}
		rawHarnesses := make([]string, len(opts.Harnesses))
		for i, target := range opts.Harnesses {
			rawHarnesses[i] = string(target)
		}
		opts.Harnesses, err = normalizeHarnesses(rawHarnesses)
		if err != nil {
			return lifecycleOptions{}, err
		}
	}

	if opts.Purge && !containsComponent(opts.Components, componentTelemetry) {
		return lifecycleOptions{}, fmt.Errorf("--purge requires telemetry in the final component selection")
	}
	if opts.RemoveCLI && !containsComponent(opts.Components, componentTelemetry) {
		return lifecycleOptions{}, fmt.Errorf("--remove-cli requires telemetry in the final component selection")
	}
	return opts, nil
}

func containsComponent(components []componentName, target componentName) bool {
	for _, component := range components {
		if component == target {
			return true
		}
	}
	return false
}

func isCompleteSelection(components []componentName) bool {
	set := make(map[componentName]bool, len(components))
	for _, component := range components {
		if !knownComponent(component) {
			return false
		}
		set[component] = true
	}
	return len(set) == len(lifecycleComponentOrder)
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
