package authdir

import "testing"

func TestConfigScalar(t *testing.T) {
	cases := []struct{ in, key, want string }{
		{"", "management_url", ""},
		{"management_url: http://127.0.0.1:9000\n", "management_url", "http://127.0.0.1:9000"},
		{"management_url: \"http://localhost:1\"\n", "management_url", "http://localhost:1"},
		{`{"management_url":"http://127.0.0.1:2"}`, "management_url", "http://127.0.0.1:2"},
		{"enabled: true\n", "management_url", ""},
	}
	for _, c := range cases {
		if got := ConfigScalar([]byte(c.in), c.key); got != c.want {
			t.Errorf("%q: got %q want %q", c.in, got, c.want)
		}
	}
}
