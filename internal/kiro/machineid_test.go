package kiro

import "testing"

func TestMachineID(t *testing.T) {
	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		name string
		cred Credential
		want string
	}{
		{"stored 64 hex", Credential{MachineID: hex64, RefreshToken: "rt-test"}, hex64},
		{"stored uuid doubled", Credential{MachineID: "01234567-89ab-cdef-0123-456789abcdef", RefreshToken: "rt-test"},
			"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"oauth from refresh token", Credential{AuthMethod: AuthMethodSocial, RefreshToken: "rt-test"},
			"0b57ce32592129d9e832f82147ef2a6cea350b58ca1e0596cfa331778daf69ff"},
		{"api key", Credential{AuthMethod: AuthMethodAPIKey, KiroAPIKey: "ksk_test"},
			"77c6119960ea126f1c3e0638e5851b977e029fd3ff89ba34ad72f4df20e856f1"},
		{"malformed stored id falls back", Credential{MachineID: "not-hex", AuthMethod: AuthMethodSocial, RefreshToken: "rt-test"},
			"0b57ce32592129d9e832f82147ef2a6cea350b58ca1e0596cfa331778daf69ff"},
	}
	for _, tc := range cases {
		if got := machineID(tc.cred); got != tc.want {
			t.Errorf("%s: got %s", tc.name, got)
		}
	}
}
