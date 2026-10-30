package add

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	topLevelKey  = regexp.MustCompile(`^[A-Za-z0-9_.-]+:`)
	servicesLine = regexp.MustCompile(`^services:\s*(#.*)?$`)
)

// ClaudeVolume keeps Claude Code's sign-in and settings in the project's
// git-ignored .neptuno folder, shared by every repo's container.
const ClaudeVolume = "./.neptuno/claude:/root/.claude"

// serviceSpec is the compose service `neptuno add` appends for a repo.
type serviceSpec struct {
	Name      string
	DependsOn []string
}

// lines renders the service with one level of indent per step.
func (s serviceSpec) lines(indent string) []string {
	in, in2 := indent, indent+indent
	out := []string{
		fmt.Sprintf("%s# Added by `neptuno add %s`", in, s.Name),
		in + s.Name + ":",
		in2 + "build: dockerfiles/" + s.Name,
		in2 + "working_dir: /app",
		fmt.Sprintf(`%svolumes: ["./repos/%s:/app", "%s"]`, in2, s.Name, ClaudeVolume),
		in2 + "env_file: .neptuno/env/" + s.Name + ".env",
		in2 + "command: sleep infinity    # overmind execs the real processes",
	}
	if len(s.DependsOn) > 0 {
		out = append(out, in2+"depends_on: ["+strings.Join(s.DependsOn, ", ")+"]")
	}
	return out
}

// insertService adds a service at the end of the services: block of a
// compose file, keeping everything else byte for byte. It matches the
// file's indentation and refuses to write anything that does not parse.
func insertService(src []byte, svc serviceSpec) ([]byte, error) {
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
		out = append(out, svc.lines("  ")...)
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
		out = append(out, svc.lines(indent)...)
		out = append(out, lines[at:]...)
	}
	result := []byte(strings.Join(out, "\n") + "\n")

	var check struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(result, &check); err != nil {
		return nil, fmt.Errorf("adding %s would leave compose.yml invalid: %w", svc.Name, err)
	}
	if _, ok := check.Services[svc.Name]; !ok {
		return nil, fmt.Errorf("could not place %s under services: in compose.yml; add it by hand", svc.Name)
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

// appendCaddySite adds an http://<name>.localhost block unless one exists.
func appendCaddySite(src []byte, name string, port int) ([]byte, bool) {
	host := "http://" + name + ".localhost"
	if bytes.Contains(src, []byte(host+" ")) || bytes.Contains(src, []byte(host+"\n")) {
		return src, false
	}
	out := bytes.TrimRight(src, "\n")
	if len(out) > 0 {
		out = append(out, "\n\n"...)
	}
	out = append(out, fmt.Sprintf("# Added by `neptuno add %s`\n%s {\n\treverse_proxy %s:%d\n}\n", name, host, name, port)...)
	return out, true
}

// composeHasService reports whether compose.yml itself, not an override
// file, defines the service.
func composeHasService(path, name string) (bool, error) {
	src, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var cf struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(src, &cf); err != nil {
		return false, fmt.Errorf("compose.yml: %w", err)
	}
	_, ok := cf.Services[name]
	return ok, nil
}
