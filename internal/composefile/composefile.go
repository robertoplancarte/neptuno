// Package composefile edits compose.yml files line by line, so comments,
// order and formatting the user chose survive.
package composefile

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	topLevelKey  = regexp.MustCompile(`^[A-Za-z0-9_.-]+:`)
	servicesLine = regexp.MustCompile(`^services:\s*(#.*)?$`)
	volumesLine  = regexp.MustCompile(`^volumes:\s*(#.*)?$`)
)

// DeclareVolumes adds named volumes to the top-level volumes: block of a
// compose file, creating it if needed, and skips ones already declared.
func DeclareVolumes(src []byte, names []string) ([]byte, error) {
	var cf struct {
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(src, &cf); err != nil {
		return nil, fmt.Errorf("compose.yml: %w", err)
	}
	var missing []string
	for _, n := range names {
		if _, ok := cf.Volumes[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) == 0 {
		return src, nil
	}

	lines := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	start := -1
	for i, l := range lines {
		if volumesLine.MatchString(l) {
			start = i
			break
		}
	}
	var out []string
	if start < 0 {
		out = append(lines, "", "volumes:")
		for _, n := range missing {
			out = append(out, "  "+n+":")
		}
	} else {
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if topLevelKey.MatchString(lines[i]) {
				end = i
				break
			}
		}
		indent := "  "
		if fc := firstContent(lines, start); fc > start && fc < end {
			if n := len(lines[fc]) - len(strings.TrimLeft(lines[fc], " ")); n > 0 {
				indent = strings.Repeat(" ", n)
			}
		}
		at := end
		for at > start+1 && isBlankOrComment(lines[at-1]) {
			at--
		}
		out = append(out, lines[:at]...)
		for _, n := range missing {
			out = append(out, indent+n+":")
		}
		out = append(out, lines[at:]...)
	}
	result := []byte(strings.Join(out, "\n") + "\n")
	if err := yaml.Unmarshal(result, &cf); err != nil {
		return nil, fmt.Errorf("declaring volumes would leave compose.yml invalid: %w", err)
	}
	for _, n := range missing {
		if _, ok := cf.Volumes[n]; !ok {
			return nil, fmt.Errorf("could not declare volume %s in compose.yml; add it by hand", n)
		}
	}
	return result, nil
}

// InsertService adds a service at the end of the services: block of a
// compose file, keeping everything else byte for byte. render returns the
// service's lines, starting with its "name:" line, for one indent step; it
// is called with the file's own indentation. InsertService refuses to
// write anything that does not parse or does not define the service.
func InsertService(src []byte, name string, render func(indent string) []string) ([]byte, error) {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}

	start := -1
	for i, l := range lines {
		if servicesLine.MatchString(l) {
			start = i
			break
		}
	}

	var out []string
	if start < 0 {
		out = lines
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, "services:")
		out = append(out, render("  ")...)
	} else {
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if topLevelKey.MatchString(lines[i]) {
				end = i
				break
			}
		}
		indent := "  "
		if fc := firstContent(lines, start); fc > start && fc < end {
			if n := len(lines[fc]) - len(strings.TrimLeft(lines[fc], " ")); n > 0 {
				indent = strings.Repeat(" ", n)
			}
		}
		// Insert after the block's last content line, before blank lines and
		// comments that belong to whatever follows.
		at := end
		for at > start+1 && isBlankOrComment(lines[at-1]) {
			at--
		}
		out = append(out, lines[:at]...)
		if at > start+1 {
			out = append(out, "")
		}
		out = append(out, render(indent)...)
		out = append(out, lines[at:]...)
	}
	result := []byte(strings.Join(out, "\n") + "\n")

	var check struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(result, &check); err != nil {
		return nil, fmt.Errorf("adding %s would leave compose.yml invalid: %w", name, err)
	}
	if _, ok := check.Services[name]; !ok {
		return nil, fmt.Errorf("could not place %s under services: in compose.yml; add it by hand", name)
	}
	return result, nil
}

// firstContent returns the index of the first non-blank, non-comment line
// after start.
func firstContent(lines []string, start int) int {
	for i := start + 1; i < len(lines); i++ {
		if !isBlankOrComment(lines[i]) {
			return i
		}
	}
	return -1
}

func isBlankOrComment(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#")
}
