package brewcli

import (
	"strings"
	"testing"
)

func TestInventoryCommandsAreDispatchedWithoutInspectingLiveHomebrewForHelp(t *testing.T) {
	for _, command := range []string{"status", "info"} {
		for _, help := range []string{"-h", "--help"} {
			code, stdout, stderr := execute("1.2.3", "0123456789abcdef0123456789abcdef01234567", command, help)
			if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "usage: brew-hextap "+command) {
				t.Fatalf("Run(%s %s) = %d, %q, %q", command, help, code, stdout, stderr)
			}
		}
	}
	code, stdout, stderr := execute("dev", "unknown", "status", "--unknown")
	if code != 2 || stdout != "" || !strings.HasPrefix(stderr, "error: status:") {
		t.Fatalf("Run(status --unknown) = %d, %q, %q", code, stdout, stderr)
	}
}

func TestTopLevelUsageListsInventoryCommands(t *testing.T) {
	code, stdout, stderr := execute("dev", "unknown", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "  status ") || !strings.Contains(stdout, "  info ") {
		t.Fatalf("Run(--help) = %d, %q, %q", code, stdout, stderr)
	}
}
