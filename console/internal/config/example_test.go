package config

import "testing"

// The shipped example is what an owner copies, so it must load as written.
func TestExampleConfigLoads(t *testing.T) {
	c, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Platform.VIP != "192.168.1.100" || c.ClusterStart.VaultNamespace != "vault" {
		t.Fatalf("%+v %+v", c.Platform, c.ClusterStart)
	}
}
