package devcli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRequireSuccessfulChecksAcceptsGreenCommitStatuses(t *testing.T) {
	// The rollup shape gh returned for PR #32 on 2026-09-27 (SB23-3094).
	rollup := `[
		{"__typename":"CheckRun","name":"Toolkit","status":"COMPLETED","conclusion":"SUCCESS"},
		{"__typename":"StatusContext","context":"GitBook (./docs)","state":"SUCCESS"}
	]`
	var checks []statusCheck
	if err := json.Unmarshal([]byte(rollup), &checks); err != nil {
		t.Fatal(err)
	}
	if err := requireSuccessfulChecks(checks); err != nil {
		t.Fatalf("requireSuccessfulChecks() error = %v", err)
	}
}

func TestRequireSuccessfulChecksRefusesNonGreenEntries(t *testing.T) {
	cases := map[string]struct {
		check statusCheck
		want  string
	}{
		"pending status": {statusCheck{Typename: "StatusContext", Context: "GitBook (./docs)", State: "PENDING"}, `status "GitBook (./docs)"`},
		"failed status":  {statusCheck{Typename: "StatusContext", Context: "ci/other", State: "FAILURE"}, `status "ci/other"`},
		"running check":  {statusCheck{Typename: "CheckRun", Name: "Toolkit", Status: "IN_PROGRESS"}, `check "Toolkit"`},
		"failed check":   {statusCheck{Typename: "CheckRun", Name: "Toolkit", Status: "COMPLETED", Conclusion: "FAILURE"}, `check "Toolkit"`},
		"untyped entry":  {statusCheck{State: "SUCCESS", Context: "unknown"}, `check ""`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := requireSuccessfulChecks([]statusCheck{tc.check})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want mention of %s", err, tc.want)
			}
		})
	}
}
