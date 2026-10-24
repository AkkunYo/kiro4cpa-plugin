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

func TestUpdateFromYAMLKeepsPacingDefaults(t *testing.T) {
	if err := UpdateFromYAML([]byte("client_version: 2.0.0\n")); err != nil {
		t.Fatal(err)
	}
	got := Get()
	if got.MinTokenInterval != defaults.MinTokenInterval ||
		got.MaxTokenInterval != defaults.MaxTokenInterval ||
		got.SuspendCooldown != defaults.SuspendCooldown {
		t.Fatalf("got %+v", got)
	}
}

func TestDurationsParsesValidValues(t *testing.T) {
	cfg := defaults
	cfg.MinTokenInterval = "250ms"
	cfg.MaxTokenInterval = "3s"
	cfg.SuspendCooldown = "30m"
	got, err := cfg.Durations()
	if err != nil {
		t.Fatal(err)
	}
	if got.MinTokenInterval.String() != "250ms" || got.MaxTokenInterval.String() != "3s" || got.SuspendCooldown.String() != "30m0s" {
		t.Fatalf("got %+v", got)
	}
}

func TestDurationsRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name              string
		min, max, cooling string
	}{
		{"bad min", "soon", "2s", "1h"},
		{"zero min", "0s", "2s", "1h"},
		{"negative min", "-1s", "2s", "1h"},
		{"bad max", "1s", "later", "1h"},
		{"max below min", "2s", "1s", "1h"},
		{"bad cooling", "1s", "2s", "forever"},
		{"zero cooling", "1s", "2s", "0s"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults
			cfg.MinTokenInterval = tc.min
			cfg.MaxTokenInterval = tc.max
			cfg.SuspendCooldown = tc.cooling
			if _, err := cfg.Durations(); err == nil {
				t.Fatalf("%+v was accepted", cfg)
			}
		})
	}
}

func TestUpdateFromYAMLRejectsInvalidPacing(t *testing.T) {
	before := Get()
	if err := UpdateFromYAML([]byte("min_token_interval: 5s\nmax_token_interval: 1s\n")); err == nil {
		t.Fatal("inverted intervals were accepted")
	}
	if got := Get(); got != before {
		t.Fatalf("rejected config changed state: %+v", got)
	}
}
