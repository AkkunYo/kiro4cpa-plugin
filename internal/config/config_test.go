package config

import "testing"

func TestUpdateFromYAMLReadsPluginBlock(t *testing.T) {
	if err := UpdateFromYAML([]byte("enabled: true\npriority: 0\nclient_version: 2.0.0\nos_version: \"\"\n")); err != nil {
		t.Fatal(err)
	}
	got := Get()
	if got.ClientVersion != "2.0.0" || got.OSVersion != defaults.OSVersion || got.DefaultRegion != "us-east-1" || !got.Enabled {
		t.Fatalf("got %+v", got)
	}
}
