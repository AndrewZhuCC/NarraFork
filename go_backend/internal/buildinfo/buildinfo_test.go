package buildinfo

import "testing"

func TestBuildInfoFallsBackToEnvWhenNotInjected(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldChannel := Channel
	Version = "go-backend-dev"
	Commit = ""
	Channel = "dev"
	t.Cleanup(func() { Version = oldVersion; Commit = oldCommit; Channel = oldChannel })
	t.Setenv("NARRAFORK_VERSION", "1.2.3")
	t.Setenv("NARRAFORK_COMMIT", "abc123")
	t.Setenv("NARRAFORK_BUILD_CHANNEL", "go-beta")

	if VersionValue() != "1.2.3" || CommitValue() != "abc123" || ChannelValue() != "go-beta" {
		t.Fatalf("unexpected env fallback version=%q commit=%q channel=%q", VersionValue(), CommitValue(), ChannelValue())
	}
}

func TestBuildInfoInjectedValuesWin(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldChannel := Channel
	Version = "9.9.9"
	Commit = "deadbeef"
	Channel = "release-test"
	t.Cleanup(func() { Version = oldVersion; Commit = oldCommit; Channel = oldChannel })
	t.Setenv("NARRAFORK_VERSION", "1.2.3")
	t.Setenv("NARRAFORK_COMMIT", "abc123")
	t.Setenv("NARRAFORK_BUILD_CHANNEL", "go-beta")

	if VersionValue() != "9.9.9" || CommitValue() != "deadbeef" || ChannelValue() != "release-test" {
		t.Fatalf("unexpected injected values version=%q commit=%q channel=%q", VersionValue(), CommitValue(), ChannelValue())
	}
}
