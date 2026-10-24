package config

import "testing"

func TestPublicOrigin(t *testing.T) {
	for _, tc := range []struct {
		origin        string
		secure, valid bool
	}{
		{"https://app.example", true, true}, {"http://localhost:5173", false, true},
		{"https://app.example/", true, false}, {"https://user:pass@app.example", true, false},
		{"http://app.example", true, false}, {"https://app.example", false, false},
		{"https://app.example?x=1", true, false}, {"", true, false},
	} {
		if got := (Config{PublicURL: tc.origin, CookieSecure: tc.secure}).Validate() == nil; got != tc.valid {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}
