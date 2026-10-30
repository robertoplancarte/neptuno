package add

import (
	"strings"
	"testing"
)

func TestInsertServiceKeepsLayout(t *testing.T) {
	src := `name: acme

services:
  proxy:
    image: caddy:2   # keep this comment

  s3:
    image: chrislusf/seaweedfs

# volumes are declared below
volumes:
  s3:
`
	got, err := insertService([]byte(src), serviceSpec{Name: "api", DependsOn: []string{"postgres", "s3"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `name: acme

services:
  proxy:
    image: caddy:2   # keep this comment

  s3:
    image: chrislusf/seaweedfs

  # Added by ` + "`neptuno add api`" + `
  api:
    build: dockerfiles/api
    working_dir: /app
    volumes: ["./repos/api:/app"]
    env_file: .neptuno/env/api.env
    command: sleep infinity    # overmind execs the real processes
    depends_on: [postgres, s3]

# volumes are declared below
volumes:
  s3:
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestInsertServiceMatchesIndent(t *testing.T) {
	src := "services:\n    db:\n        image: postgres:17\n"
	got, err := insertService([]byte(src), serviceSpec{Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\n    web:\n        build: dockerfiles/web\n") {
		t.Errorf("indent not matched:\n%s", got)
	}
	if strings.Contains(string(got), "depends_on") {
		t.Errorf("depends_on written with no dependencies:\n%s", got)
	}
}

func TestInsertServiceWithoutServicesKey(t *testing.T) {
	got, err := insertService([]byte("name: acme\n"), serviceSpec{Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "name: acme\n\nservices:\n  # Added") {
		t.Errorf("got:\n%s", got)
	}
	if _, err := insertService(nil, serviceSpec{Name: "web"}); err != nil {
		t.Errorf("empty compose.yml: %v", err)
	}
}

func TestInsertServiceRefusesInvalidResult(t *testing.T) {
	if _, err := insertService([]byte("services: {}\n"), serviceSpec{Name: "web"}); err == nil {
		t.Error("flow-style services was rewritten instead of refused")
	}
}

func TestAppendCaddySite(t *testing.T) {
	src := []byte("http://s3.localhost {\n\treverse_proxy s3:8333\n}\n")
	got, changed := appendCaddySite(src, "api", 3000)
	if !changed {
		t.Fatal("site not added")
	}
	want := "http://s3.localhost {\n\treverse_proxy s3:8333\n}\n\n# Added by `neptuno add api`\nhttp://api.localhost {\n\treverse_proxy api:3000\n}\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, changed := appendCaddySite(got, "api", 3000); changed {
		t.Error("site added twice")
	}
}
