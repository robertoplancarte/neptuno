package stacks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinStacksRender(t *testing.T) {
	list, err := List("")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, s := range list {
		names[s.Name] = true
		data := Data{Name: "my-app", Project: "acme", Port: s.Port}
		if s.Port == 0 {
			t.Errorf("%s: no port", s.Name)
		}
		for _, f := range []string{"Dockerfile", "Procfile", "base.env"} {
			out, err := s.Render(f, data)
			if err != nil {
				t.Errorf("%s/%s: %v", s.Name, f, err)
				continue
			}
			if strings.Contains(string(out), "{{") {
				t.Errorf("%s/%s left a template action unrendered", s.Name, f)
			}
		}
		if _, err := s.AppFiles(data); err != nil {
			t.Errorf("%s app files: %v", s.Name, err)
		}
		if _, err := s.GenerateCommand(data); err != nil {
			t.Errorf("%s generate: %v", s.Name, err)
		}
	}
	for _, want := range []string{"blank", "go", "node", "python", "rails"} {
		if !names[want] {
			t.Errorf("built-in stack %s missing", want)
		}
	}
}

func TestAppFilesDropTmplSuffix(t *testing.T) {
	s, err := Get("", "go")
	if err != nil {
		t.Fatal(err)
	}
	files, err := s.AppFiles(Data{Name: "svc", Port: 9000})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(files["go.mod"]); got != "module svc\n\ngo 1.24\n" {
		t.Errorf("go.mod = %q", got)
	}
	if !strings.Contains(string(files["main.go"]), `port = "9000"`) {
		t.Errorf("main.go not rendered:\n%s", files["main.go"])
	}
}

func TestProjectStackOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	write := func(path, content string) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("node/stack.yml", "description: our node\nport: 4000\n")
	write("node/Procfile", "web: yarn dev\n")
	write("elixir/stack.yml", "description: phoenix\nport: 4000\n")
	write("notastack/README", "no stack.yml here\n")

	s, err := Get(dir, "node")
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 4000 || s.Source != filepath.Join(dir, "node") {
		t.Errorf("project stack did not override built-in: %+v", s)
	}
	if out, _ := s.Render("Procfile", Data{}); string(out) != "web: yarn dev\n" {
		t.Errorf("Procfile = %q", out)
	}
	if _, err := Get(dir, "elixir"); err != nil {
		t.Errorf("project-only stack: %v", err)
	}
	if _, err := Get(dir, "notastack"); err == nil {
		t.Error("folder without stack.yml counted as a stack")
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExtendsBuiltinOfSameName(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"rails/stack.yml": "extends: rails\ndescription: Rails with Tailwind\ngenerate: rails new . --css=tailwind\n",
		"rails/Procfile":  "web: bin/rails s\ncss: bin/rails tailwindcss:watch\n",
	})
	s, err := Get(dir, "rails")
	if err != nil {
		t.Fatal(err)
	}
	if s.Description != "Rails with Tailwind" || s.Generate != "rails new . --css=tailwind" {
		t.Errorf("own settings not applied: %+v", s)
	}
	if s.Port != 3000 || len(s.Services) != 3 {
		t.Errorf("settings not inherited: port %d services %v", s.Port, s.Services)
	}
	if out, _ := s.Render("Procfile", Data{}); !strings.Contains(string(out), "tailwindcss:watch") {
		t.Errorf("own Procfile not used: %s", out)
	}
	if out, err := s.Render("Dockerfile", Data{Name: "shop"}); err != nil || !strings.Contains(string(out), "FROM ruby:3.3") {
		t.Errorf("Dockerfile not inherited: %v %s", err, out)
	}
}

func TestExtendsChainAndAppFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		// node-ts extends the built-in node and adds a file; api extends
		// node-ts and clears the services.
		"node-ts/stack.yml":         "extends: node\nport: 5173\n",
		"node-ts/app/tsconfig.json": "{}\n",
		"node-ts/app/server.js":     "// {{.Name}} in TypeScript soon\n",
		"api/stack.yml":             "extends: node-ts\nservices: []\n",
	})
	s, err := Get(dir, "api")
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 5173 || len(s.Services) != 0 {
		t.Errorf("port %d services %v", s.Port, s.Services)
	}
	files, err := s.AppFiles(Data{Name: "x", Port: 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(files["server.js"]) != "// x in TypeScript soon\n" {
		t.Errorf("child app file did not win: %q", files["server.js"])
	}
	for _, f := range []string{"package.json", ".gitignore", "tsconfig.json"} {
		if _, ok := files[f]; !ok {
			t.Errorf("app/%s missing from merged files", f)
		}
	}
}

func TestExtendsErrors(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"unknown parent":    {"a/stack.yml": "extends: nope\n"},
		"cycle":             {"a/stack.yml": "extends: b\n", "b/stack.yml": "extends: a\n"},
		"self, no built-in": {"mine/stack.yml": "extends: mine\n"},
	} {
		dir := t.TempDir()
		writeFiles(t, dir, files)
		if _, err := List(dir); err == nil {
			t.Errorf("%s: List succeeded", name)
		}
	}
}

func TestCopy(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"rails/stack.yml": "extends: rails\ngenerate: rails new . --css=tailwind\n",
	})
	s, err := Get(dir, "rails")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "rails-copy")
	paths, err := s.Copy(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(paths, ",") != "Dockerfile,Procfile,base.env,entrypoint.sh,stack.yml" {
		t.Errorf("copied %v", paths)
	}
	yml, _ := os.ReadFile(filepath.Join(target, "stack.yml"))
	if strings.Contains(string(yml), "extends") || !strings.Contains(string(yml), "--css=tailwind") || !strings.Contains(string(yml), "port: 3000") {
		t.Errorf("stack.yml not merged and standalone:\n%s", yml)
	}

	// The copy works on its own as a project stack.
	copied, err := Get(filepath.Dir(target), "rails-copy")
	if err != nil {
		t.Fatal(err)
	}
	if copied.Generate != s.Generate || copied.Port != s.Port {
		t.Errorf("copy resolves differently: %+v", copied)
	}
	if _, err := s.Copy(target); err == nil {
		t.Error("Copy overwrote an existing folder")
	}
}

func TestRailsOptions(t *testing.T) {
	s, err := Get("", "rails")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.OptionNames(), ","); got != "action_text,storage_s3,tailwind" {
		t.Errorf("options = %s", got)
	}

	// Defaults: tailwind only.
	data, err := s.Select(Data{Name: "shop", Port: 3000}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen, _ := s.GenerateCommand(data)
	if !strings.HasSuffix(gen, "--skip-thruster --css=tailwind") {
		t.Errorf("default generate = %s", gen)
	}
	proc, _ := s.Render("Procfile", data)
	if !strings.Contains(string(proc), "\ncss: bin/rails tailwindcss:watch\n") {
		t.Errorf("Procfile:\n%s", proc)
	}

	data, err = s.Select(Data{Name: "shop", Port: 3000}, []string{"action_text", "storage_s3"}, []string{"tailwind"})
	if err != nil {
		t.Fatal(err)
	}
	gen, _ = s.GenerateCommand(data)
	if strings.Contains(gen, "tailwind") || !strings.Contains(gen, ") && (bin/rails action_text:install) && (bundle add aws-sdk-s3") {
		t.Errorf("generate = %s", gen)
	}
	if out, err := exec.Command("sh", "-n", "-c", gen).CombinedOutput(); err != nil {
		t.Errorf("generate command is not valid sh: %v\n%s", err, out)
	}
	env, _ := s.Render("base.env", data)
	if !strings.Contains(string(env), "# From the storage_s3 option\nAWS_ENDPOINT=http://s3:8333\n") || !strings.Contains(string(env), "S3_BUCKET=shop-development\n") {
		t.Errorf("base.env:\n%s", env)
	}
	proc, _ = s.Render("Procfile", data)
	if strings.Contains(string(proc), "tailwind") {
		t.Errorf("Procfile has tailwind although it is off:\n%s", proc)
	}

	if _, err := s.Select(Data{}, []string{"sidekiq"}, nil); err == nil || !strings.Contains(err.Error(), "options: action_text, storage_s3, tailwind") {
		t.Errorf("unknown option error = %v", err)
	}
}

func TestOptionsMergeThroughExtends(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"rails/stack.yml": "extends: rails\noptions:\n  tailwind:\n    description: Tailwind, off by default here\n  sidekiq:\n    procfile: [\"jobs: bundle exec sidekiq\"]\n    services: [valkey]\n",
	})
	s, err := Get(dir, "rails")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(s.OptionNames(), ","); got != "action_text,sidekiq,storage_s3,tailwind" {
		t.Errorf("options = %s", got)
	}
	data, _ := s.Select(Data{Name: "shop"}, []string{"sidekiq"}, nil)
	if data.Options["tailwind"] {
		t.Error("the override's tailwind should not default to on")
	}
	if gen, _ := s.GenerateCommand(data); strings.Contains(gen, "tailwind") {
		t.Errorf("overridden tailwind still adds its flag: %s", gen)
	}
	if proc, _ := s.Render("Procfile", data); !strings.Contains(string(proc), "jobs: bundle exec sidekiq\n") {
		t.Errorf("Procfile:\n%s", proc)
	}

	copied := filepath.Join(t.TempDir(), "rails-copy")
	if _, err := s.Copy(copied); err != nil {
		t.Fatal(err)
	}
	c, err := Get(filepath.Dir(copied), "rails-copy")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.OptionNames(), ","); got != "action_text,sidekiq,storage_s3,tailwind" {
		t.Errorf("copied options = %s", got)
	}
}
