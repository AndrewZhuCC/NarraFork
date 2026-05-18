package git

import "testing"

func TestRemoteURLWithoutCredentialsStripsUserInfo(t *testing.T) {
	got := RemoteURLWithoutCredentials("https://user:secret@example.com/org/repo.git")
	if got != "https://example.com/org/repo.git" {
		t.Fatalf("sanitized URL = %q", got)
	}
	credentials := cloneCredentialsFromURL("https://user:secret@example.com/org/repo.git")
	if credentials == nil || credentials.Username != "user" || credentials.Password != "secret" {
		t.Fatalf("credentials = %#v", credentials)
	}
}

func TestMaskCloneSecretsRemovesCredentialsFromProgress(t *testing.T) {
	line := "fatal: Authentication failed for 'https://user:secret@example.com/org/repo.git'"
	got := maskCloneSecrets(line, "https://user:secret@example.com/org/repo.git", &CloneCredentials{Username: "user", Password: "secret"})
	if got == line || got == "" {
		t.Fatalf("expected line to be masked, got %q", got)
	}
	if containsAny(got, []string{"secret", "user:secret@"}) {
		t.Fatalf("masked line still contains credentials: %q", got)
	}
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && len(value) >= len(needle) {
			for i := 0; i+len(needle) <= len(value); i++ {
				if value[i:i+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}
