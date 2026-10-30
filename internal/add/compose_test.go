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
    volumes: ["./repos/api:/app", "./.neptuno/claude:/root/.claude"]
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
	got, did := caddySite(src, "api", 3000, false)
	if did != "added" {
		t.Fatal("site not added")
	}
	want := "http://s3.localhost {\n\treverse_proxy s3:8333\n}\n\n# Added by `neptuno add api`\nhttp://api.localhost {\n\treverse_proxy api:3000\n}\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if _, did := caddySite(got, "api", 3000, false); did != "" {
		t.Error("site added twice")
	}

	// --wildcard on an existing repo widens its site.
	got, did = caddySite(got, "api", 3000, true)
	if did != "updated" || !strings.Contains(string(got), "\nhttp://api.localhost, http://*.api.localhost {\n\treverse_proxy api:3000\n}\n") {
		t.Errorf("%s:\n%s", did, got)
	}
	if _, did := caddySite(got, "api", 3000, true); did != "" {
		t.Error("wildcard added twice")
	}

	got, did = caddySite(src, "shop", 3000, true)
	if did != "added" || !strings.Contains(string(got), "\nhttp://shop.localhost, http://*.shop.localhost {\n") {
		t.Errorf("%s:\n%s", did, got)
	}
}

func TestDeclareVolumes(t *testing.T) {
	src := "services:\n  db:\n    image: postgres\n\nvolumes:\n  postgres:\n\n# trailing comment\n"
	got, err := declareVolumes([]byte(src), []string{"postgres", "shop_bundle"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "services:\n  db:\n    image: postgres\n\nvolumes:\n  postgres:\n  shop_bundle:\n\n# trailing comment\n"; string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	got, err = declareVolumes([]byte("services:\n  db:\n    image: postgres\n"), []string{"shop_bundle"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(got), "\nvolumes:\n  shop_bundle:\n") {
		t.Errorf("no volumes block created:\n%s", got)
	}

	if names := namedVolumes([]string{"shop_bundle:/usr/local/bundle", "./repos/shop:/app", "/tmp:/tmp", "~/x:/x"}); strings.Join(names, ",") != "shop_bundle" {
		t.Errorf("namedVolumes = %v", names)
	}
}
