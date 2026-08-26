package skillinstall

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeMarkerRejectsAmbiguousAndUnsafeJSON(t *testing.T) {
	valid, err := encodeMarker("hextap", "1.0.0", []bundleFile{{name: "SKILL.md", data: []byte("skill\n")}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "duplicate root key",
			data: []byte(strings.Replace(string(valid), `"schema": 1,`, `"schema": 1, "schema": 1,`, 1)),
			want: "duplicate",
		},
		{
			name: "duplicate nested key",
			data: []byte(strings.Replace(string(valid), `"path": "SKILL.md",`, `"path": "SKILL.md", "path": "SKILL.md",`, 1)),
			want: "duplicate",
		},
		{
			name: "unknown field",
			data: []byte(strings.Replace(string(valid), `"schema": 1,`, `"schema": 1, "unknown": true,`, 1)),
			want: "unknown field",
		},
		{
			name: "trailing JSON",
			data: append(append([]byte(nil), valid...), []byte("{}\n")...),
			want: "trailing JSON",
		},
		{
			name: "oversized marker",
			data: bytes.Repeat([]byte("x"), maxMarkerSize+1),
			want: "exceeds",
		},
		{
			name: "unsafe path",
			data: []byte(strings.Replace(string(valid), `"path": "SKILL.md"`, `"path": "../SKILL.md"`, 1)),
			want: "unsafe",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeMarker(test.data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decodeMarker() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestMarkerAndManagedFileHardeningFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, home, skillDir string)
		want    State
	}{
		{
			name: "oversized marker",
			prepare: func(t *testing.T, _, skillDir string) {
				if err := os.MkdirAll(skillDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(skillDir, markerFileName), bytes.Repeat([]byte("x"), maxMarkerSize+1), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: InvalidState,
		},
		{
			name: "marker symlink",
			prepare: func(t *testing.T, home, skillDir string) {
				if err := os.MkdirAll(skillDir, 0o755); err != nil {
					t.Fatal(err)
				}
				marker, err := encodeMarker("hextap", "1.0.0", []bundleFile{{name: "SKILL.md", data: []byte("skill\n")}})
				if err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(home, "external-marker.json")
				if err := os.WriteFile(external, marker, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(skillDir, markerFileName)); err != nil {
					t.Fatal(err)
				}
			},
			want: InvalidState,
		},
		{
			name: "managed file symlink",
			prepare: func(t *testing.T, home, skillDir string) {
				installCurrentFixture(t, home)
				skillPath := filepath.Join(skillDir, "SKILL.md")
				data, err := os.ReadFile(skillPath)
				if err != nil {
					t.Fatal(err)
				}
				external := filepath.Join(home, "external-skill.md")
				if err := os.WriteFile(external, data, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(skillPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, skillPath); err != nil {
					t.Fatal(err)
				}
			},
			want: DriftedState,
		},
		{
			name: "noncanonical marker mode",
			prepare: func(t *testing.T, home, skillDir string) {
				installCurrentFixture(t, home)
				if err := os.Chmod(filepath.Join(skillDir, markerFileName), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: DriftedState,
		},
		{
			name: "noncanonical managed mode",
			prepare: func(t *testing.T, home, skillDir string) {
				installCurrentFixture(t, home)
				if err := os.Chmod(filepath.Join(skillDir, "SKILL.md"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: DriftedState,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			target := targetByIDForTest(t, "claude-code")
			skillDir := filepath.Join(home, target.UserSkillsDir, "hextap")
			test.prepare(t, home, skillDir)
			options := Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}
			status, err := Status(options)
			if err != nil || len(status.Entries) != 1 || status.Entries[0].State != test.want {
				t.Fatalf("Status() = %#v, %v, want %s", status, err, test.want)
			}
			if _, err := Install(options); err == nil {
				t.Fatalf("Install() accepted %s target", test.want)
			}
		})
	}
}

func installCurrentFixture(t *testing.T, home string) {
	t.Helper()
	if _, err := Install(Options{Agents: []string{"claude-code"}, Scope: UserScope, HomeDir: home}); err != nil {
		t.Fatalf("Install(current fixture): %v", err)
	}
}
