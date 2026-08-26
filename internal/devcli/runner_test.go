package devcli

import (
	"strings"
	"testing"
)

func TestCommandEnvironmentScrubsGitConfigInjectionAndAppliesPinnedHost(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
	t.Setenv("GIT_CONFIG_VALUE_0", "malicious")
	t.Setenv("GH_HOST", "untrusted.example")
	environment := commandEnvironment(map[string]string{"GH_HOST": "github.com"})
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_0=", "GIT_CONFIG_VALUE_0=", "GH_HOST=untrusted.example"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("command environment retained %q", forbidden)
		}
	}
	if !strings.Contains(joined, "GH_HOST=github.com") {
		t.Fatalf("command environment did not pin GitHub host: %s", joined)
	}
	for index := 1; index < len(environment); index++ {
		if environment[index-1] > environment[index] {
			t.Fatal("command environment is not deterministic")
		}
	}
}

func TestLimitedBufferBoundsOutputWithoutShortWrite(t *testing.T) {
	var buffer limitedBuffer
	payload := make([]byte, maxCommandOutput+1)
	written, err := buffer.Write(payload)
	if err != nil || written != len(payload) || !buffer.overflow || len(buffer.String()) != maxCommandOutput {
		t.Fatalf("limited buffer = written %d, error %v, overflow %t, length %d", written, err, buffer.overflow, len(buffer.String()))
	}
}
