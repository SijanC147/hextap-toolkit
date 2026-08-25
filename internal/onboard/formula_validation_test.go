package onboard

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	formulaengine "github.com/SijanC147/hextap-toolkit/internal/formula"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

func TestValidateFormulaClassRejectsSpoofsAndAdditionalClasses(t *testing.T) {
	tests := map[string]string{
		"comment only":         "# class ExampleTool < Formula\n",
		"block comment only":   "=begin\nclass ExampleTool < Formula\n=end\n",
		"heredoc only":         "value = <<~EOS\nclass ExampleTool < Formula\nEOS\n",
		"string only":          "value = \"class ExampleTool < Formula\"\n",
		"dead nested only":     "if false\n  class ExampleTool < Formula\n  end\nend\n",
		"dead unindented only": "if false\nclass ExampleTool < Formula\nend\n",
		"end data only":        "__END__\nclass ExampleTool < Formula\nend\n",
		"indented only":        "  class ExampleTool < Formula\n  end\n",
		"duplicate expected":   "class ExampleTool < Formula\nend\nclass ExampleTool < Formula\nend\n",
		"alternate top-level": "class ExampleTool < Formula\nend\n" +
			"class OtherTool < Formula\nend\n",
		"alternate namespaced": "class ExampleTool < Formula\nend\n" +
			"class Evil::OtherTool < Formula\nend\n",
		"alternate nested": "class ExampleTool < Formula\n" +
			"  if false\n    class OtherTool < Formula\n    end\n  end\nend\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateFormulaClass([]byte(source), "ExampleTool"); err == nil {
				t.Fatal("validateFormulaClass() unexpectedly succeeded")
			}
		})
	}
}

func TestValidateFormulaClassAcceptsOneGenuineDeclarationAndIgnoresSpoofText(t *testing.T) {
	source := `# class Wrong < Formula
=begin
class Wrong < Formula
=end
message = "class Wrong < Formula"
class ExampleTool < Formula
  def caveats
    <<~EOS
      class Wrong < Formula
    EOS
  end
end
`
	if err := validateFormulaClass([]byte(source), "ExampleTool"); err != nil {
		t.Fatalf("validateFormulaClass() = %v", err)
	}
}

func TestValidateFormulaClassAcceptsRenderedFormulaWithCaveats(t *testing.T) {
	root := writeGoProject(t)
	if _, err := Onboard(validOptions(root)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".hextap.json"))
	if err != nil {
		t.Fatal(err)
	}
	project, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	project.Homebrew.Caveats = "class Wrong < Formula\nKeep the real class structural."
	rendered, err := formulaengine.Render(
		project,
		"1.2.3",
		string(bytes.Repeat([]byte("a"), 64)),
		string(bytes.Repeat([]byte("b"), 64)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFormulaClass(rendered, project.Formula.Class); err != nil {
		t.Fatalf("validateFormulaClass(rendered) = %v\n%s", err, rendered)
	}
}
