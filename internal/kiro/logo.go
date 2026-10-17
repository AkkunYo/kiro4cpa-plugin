package kiro

import (
	_ "embed"
	"encoding/base64"
)

//go:embed assets/kiro-logo.svg
var kiroLogoSVG []byte

func logoDataURI() string {
	if len(kiroLogoSVG) == 0 {
		return ""
	}
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(kiroLogoSVG)
}
