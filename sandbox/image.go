package sandbox

import (
	_ "embed"
	"strings"
)

//go:embed dockerfile_version.txt
var rawVersion string

const imageRepo = "pcapchu/sandbox"

// ImageName returns the full Docker image reference (repo:tag)
// using the version embedded from dockerfile_version.txt.
func ImageName() string {
	return imageRepo + ":" + strings.TrimSpace(rawVersion)
}
