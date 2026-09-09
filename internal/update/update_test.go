package update

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, cand string
		want      bool
	}{{"0.7.4", "0.7.5", true}, {"0.7.4", "v0.8.0", true}, {"0.7.4", "0.7.4", false}, {"0.7.4", "0.7.3", false}, {"1.0.0", "0.9.9", false}, {"dev", "9.9.9", false}, {"0.7.4-rc1", "0.7.4", false}}
	for _, c := range cases {
		if got := Newer(c.cur, c.cand); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.cand, got, c.want)
		}
	}
}
