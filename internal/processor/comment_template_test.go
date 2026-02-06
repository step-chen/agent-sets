package processor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateLoader_Load(t *testing.T) {
	// Create temporary directory structure for testing
	tmpDir := t.TempDir()
	commentsDir := filepath.Join(tmpDir, "comments")
	componentsDir := filepath.Join(commentsDir, "components")

	if err := os.MkdirAll(componentsDir, 0755); err != nil {
		t.Fatalf("failed to create temp dirs: %v", err)
	}

	// Create dummy templates
	files := map[string]string{
		"comments/summary.tmpl":           "Summary: {{.Content}} | Marker: {{template \"marker.tmpl\" .MarkerData}}",
		"comments/inline.tmpl":            "Inline: {{.Content}} | Footer: {{template \"footer.tmpl\" .FooterData}}",
		"comments/addons.tmpl":            "Addons: {{.TableData}}",
		"comments/components/marker.tmpl": "MARKER-{{.Type}}",
		"comments/components/footer.tmpl": "FOOTER-{{.Model}}",
		"comments/components/table.tmpl":  "TABLE", // Not used in this simple test but required for full setup
	}

	for path, content := range files {
		fullPath := filepath.Join(tmpDir, path)
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file %s: %v", path, err)
		}
	}

	loader := NewTemplateLoader(tmpDir)
	templates, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if templates.Summary == nil || templates.Inline == nil || templates.Addons == nil {
		t.Fatal("expected all templates to be loaded")
	}

	// Test execution
	t.Run("Summary Execution", func(t *testing.T) {
		var buf bytes.Buffer
		data := SummaryTemplateData{
			Content:    "TestContent",
			MarkerData: MarkerData{Type: "summary"},
		}
		if err := templates.Summary.Execute(&buf, data); err != nil {
			t.Errorf("failed to execute summary template: %v", err)
		}
		expected := "Summary: TestContent | Marker: MARKER-summary"
		if buf.String() != expected {
			t.Errorf("expected %q, got %q", expected, buf.String())
		}
	})

	t.Run("Inline Execution", func(t *testing.T) {
		var buf bytes.Buffer
		data := InlineTemplateData{
			Content:    "TestInline",
			FooterData: FooterData{Model: "gpt"},
		}
		if err := templates.Inline.Execute(&buf, data); err != nil {
			t.Errorf("failed to execute inline template: %v", err)
		}
		expected := "Inline: TestInline | Footer: FOOTER-gpt"
		if buf.String() != expected {
			t.Errorf("expected %q, got %q", expected, buf.String())
		}
	})
}
