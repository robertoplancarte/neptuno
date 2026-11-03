package add

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/robertoplancarte/neptuno/internal/composefile"
)

// ClaudeVolume keeps Claude Code's sign-in and settings in the project's
// git-ignored .neptuno folder, shared by every repo's container.
const ClaudeVolume = "./.neptuno/claude:/root/.claude"

// serviceSpec is the compose service `neptuno add` appends for a repo.
type serviceSpec struct {
	Name      string
	DependsOn []string
	// Volumes are mounted after the repo and Claude Code's config.
	Volumes []string
}

// lines renders the service with one level of indent per step.
func (s serviceSpec) lines(indent string) []string {
	in, in2 := indent, indent+indent
	out := []string{
		fmt.Sprintf("%s# Added by `neptuno add %s`", in, s.Name),
		in + s.Name + ":",
		in2 + "build: dockerfiles/" + s.Name,
		in2 + "working_dir: /app",
		fmt.Sprintf(`%svolumes: [%s]`, in2, quoteList(append([]string{"./repos/" + s.Name + ":/app", ClaudeVolume}, s.Volumes...))),
		in2 + "env_file: .neptuno/env/" + s.Name + ".env",
		in2 + "command: sleep infinity    # overmind execs the real processes",
	}
	if len(s.DependsOn) > 0 {
		out = append(out, in2+"depends_on: ["+strings.Join(s.DependsOn, ", ")+"]")
	}
	return out
}

func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, it := range items {
		quoted[i] = strconv.Quote(it)
	}
	return strings.Join(quoted, ", ")
}

// namedVolumes returns the named volumes among compose volume specs, the
// ones that are neither a path nor anonymous.
func namedVolumes(specs []string) []string {
	var out []string
	for _, v := range specs {
		name, _, ok := strings.Cut(v, ":")
		if ok && name != "" && !strings.ContainsAny(name[:1], "./~$") {
			out = append(out, name)
		}
	}
	return out
}

// insertService adds a repo's service to a compose file.
func insertService(src []byte, svc serviceSpec) ([]byte, error) {
	return composefile.InsertService(src, svc.Name, svc.lines)
}

// declareVolumes adds named volumes to a compose file's volumes: block.
func declareVolumes(src []byte, names []string) ([]byte, error) {
	return composefile.DeclareVolumes(src, names)
}

// caddySite adds an http://<name>.localhost block unless one exists. With
// wildcard, the block also answers http://*.<name>.localhost, such as one
// subdomain per tenant, and an existing block without it gains it. It
// returns what it did: "added", "updated" or "" for nothing.
func caddySite(src []byte, name string, port int, wildcard bool) ([]byte, string) {
	host := "http://" + name + ".localhost"
	wild := "http://*." + name + ".localhost"
	lines := strings.Split(string(src), "\n")
	for i, l := range lines {
		addr, rest, ok := strings.Cut(l, "{")
		if !ok || strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		hosts := strings.FieldsFunc(addr, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
		if !slices.Contains(hosts, host) {
			continue
		}
		if !wildcard || slices.Contains(hosts, wild) {
			return src, ""
		}
		lines[i] = strings.TrimRight(addr, " \t") + ", " + wild + " {" + rest
		return []byte(strings.Join(lines, "\n")), "updated"
	}

	address := host
	if wildcard {
		address += ", " + wild
	}
	out := bytes.TrimRight(src, "\n")
	if len(out) > 0 {
		out = append(out, "\n\n"...)
	}
	out = append(out, fmt.Sprintf("# Added by `neptuno add %s`\n%s {\n\treverse_proxy %s:%d\n}\n", name, address, name, port)...)
	return out, "added"
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
