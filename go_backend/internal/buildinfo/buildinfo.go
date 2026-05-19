package buildinfo

import (
	"os"
	"strings"
)

const Backend = "go"

var Version = "go-backend-dev"
var Commit = ""
var Channel = "dev"

func VersionValue() string {
	if value := strings.TrimSpace(Version); value != "" && value != "go-backend-dev" {
		return value
	}
	for _, key := range []string{"NARRAFORK_VERSION", "APP_VERSION"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "go-backend-dev"
}

func CommitValue() string {
	if value := strings.TrimSpace(Commit); value != "" {
		return value
	}
	for _, key := range []string{"NARRAFORK_COMMIT", "GIT_COMMIT"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func ChannelValue() string {
	if value := strings.TrimSpace(Channel); value != "" && value != "dev" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("NARRAFORK_BUILD_CHANNEL")); value != "" {
		return value
	}
	return "dev"
}
