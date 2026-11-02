package proxy

import (
	"reflect"
	"testing"
)

const caddyfile = `# Caddyfile for acme
{
	admin off
}

(common) {
	encode gzip
}

http://s3.localhost {
	reverse_proxy s3:8333
}

# Added by ` + "`neptuno add web`" + `
http://web.localhost, http://*.web.localhost {
	reverse_proxy web:3000
}

http://shop.localhost {
	handle /api/* {
		reverse_proxy /api/* http://api:4000 {
			header_up Host {host}
		}
	}
	reverse_proxy shop:5000
}

http://docs.localhost {
	reverse_proxy host.docker.internal:8000
}

api.example.test{
	reverse_proxy api:4000
}
`

func TestParse(t *testing.T) {
	got := Parse(caddyfile)
	want := []Site{
		{Addresses: []string{"http://s3.localhost"}, Upstreams: []string{"s3"}},
		{Addresses: []string{"http://web.localhost", "http://*.web.localhost"}, Upstreams: []string{"web"}},
		{Addresses: []string{"http://shop.localhost"}, Upstreams: []string{"api", "shop"}},
		{Addresses: []string{"http://docs.localhost"}, Upstreams: []string{"host.docker.internal"}},
		{Addresses: []string{"api.example.test"}, Upstreams: []string{"api"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse =\n%+v\nwant\n%+v", got, want)
	}
}

func TestURLs(t *testing.T) {
	sites := Parse(caddyfile)
	cases := []struct {
		service   string
		published map[int]int
		want      []Link
	}{
		{"web", nil, []Link{{URL: "http://web.localhost"}, {URL: "http://*.web.localhost", Pattern: true}}},
		{"web", map[int]int{80: 8080}, []Link{{URL: "http://web.localhost:8080"}, {URL: "http://*.web.localhost:8080", Pattern: true}}},
		{"api", nil, []Link{{URL: "http://shop.localhost"}, {URL: "https://api.example.test"}}},
		// A host-run repo is proxied through the host, so match its name.
		{"docs", nil, []Link{{URL: "http://docs.localhost"}}},
		{"postgres", nil, nil},
	}
	for _, c := range cases {
		if got := URLs(sites, c.service, c.published); !reflect.DeepEqual(got, c.want) {
			t.Errorf("URLs(%s, %v) = %+v, want %+v", c.service, c.published, got, c.want)
		}
	}
}

func TestLink(t *testing.T) {
	cases := map[string]string{
		"http://web.localhost":    "http://web.localhost",
		"web.localhost":           "https://web.localhost",
		"web.localhost:80":        "http://web.localhost",
		"https://web.localhost":   "https://web.localhost",
		":8080":                   "http://localhost:8080",
		"localhost:3000":          "https://localhost:3000",
		"http://web.localhost/a*": "http://web.localhost/a",
	}
	for addr, want := range cases {
		if got := link(addr, nil).URL; got != want {
			t.Errorf("link(%q) = %q, want %q", addr, got, want)
		}
	}
}
