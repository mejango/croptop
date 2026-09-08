package config

import "testing"

func TestPasscodeAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := Load(dir)
	if err != nil || c.Listen != DefaultListen || c.HasPasscode() {
		t.Fatalf("fresh config: %+v %v", c, err)
	}
	c.SetPasscode("x")
	c.Listen = "0.0.0.0:8086"
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.CheckPasscode("x") || c2.CheckPasscode("y") || c2.Listen != "0.0.0.0:8086" {
		t.Fatalf("round trip: %+v", c2)
	}
}
