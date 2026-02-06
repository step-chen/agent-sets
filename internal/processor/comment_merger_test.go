package processor

import (
	"strings"
	"testing"
	"text/template"

	"pr-review-automation/internal/config"
	"pr-review-automation/internal/domain"
)

func createTestTemplates() *CommentTemplates {
	return &CommentTemplates{
		Inline:  template.Must(template.New("inline").Parse("INLINE: {{.Title}} {{range .TableData.Rows}}|{{index . 2}}{{end}}")),
		Addons:  template.Must(template.New("addons").Parse("ADDONS: {{range .TableData.Rows}}|{{index . 1}}{{end}}")),
		Summary: template.Must(template.New("summary").Parse("SUMMARY")),
	}
}

func TestCommentMerger_Merge(t *testing.T) {
	cfg := &config.CommentMergeConfig{
		Enabled:           true,
		HighSeverityMerge: "by_file",
		LowSeverityMerge:  "to_summary",
	}
	merger := NewCommentMerger(cfg, "", nil, false)

	comments := []domain.ReviewComment{
		{File: "a.go", Line: 10, Severity: "WARNING", Comment: "Warn A"},
		{File: "a.go", Line: 20, Severity: "CRITICAL", Comment: "Crit A"},
		{File: "b.go", Line: 5, Severity: "WARNING", Comment: "Warn B"},
		{File: "b.go", Line: 15, Severity: "INFO", Comment: "Info B"},
		{File: "c.go", Line: 1, Severity: "NIT", Comment: "Nit C"},
	}

	result := merger.Merge(comments, "commit123")

	// Verify FileComments (High Severity)
	if len(result.FileComments) != 2 {
		t.Errorf("expected 2 file comments, got %d", len(result.FileComments))
	}

	// a.go
	fcA := result.FileComments[0]
	if fcA.FilePath != "a.go" {
		t.Errorf("expected a.go, got %s", fcA.FilePath)
	}
	if len(fcA.Comments) != 2 {
		t.Errorf("expected 2 comments in a.go, got %d", len(fcA.Comments))
	}
	if fcA.Comments[0].Comment != "Warn A" { // Line 10
		t.Errorf("expected Warn A first (line 10), got %s", fcA.Comments[0].Comment)
	}

	// b.go
	fcB := result.FileComments[1]
	if fcB.FilePath != "b.go" {
		t.Errorf("expected b.go, got %s", fcB.FilePath)
	}
	// Warning only, Info should be in summary
	if len(fcB.Comments) != 1 {
		t.Errorf("expected 1 comment in b.go, got %d", len(fcB.Comments))
	}
	if fcB.Comments[0].Comment != "Warn B" {
		t.Errorf("expected Warn B, got %s", fcB.Comments[0].Comment)
	}

	// Verify SummaryAddons (Low Severity)
	if len(result.SummaryAddons) != 2 {
		t.Errorf("expected 2 summary addons, got %d", len(result.SummaryAddons))
	}
	// Info B and Nit C
	foundInfoB := false
	foundNitC := false
	for _, c := range result.SummaryAddons {
		if c.Comment == "Info B" {
			foundInfoB = true
		}
		if c.Comment == "Nit C" {
			foundNitC = true
		}
	}
	if !foundInfoB || !foundNitC {
		t.Error("missing low severity comments in summary addons")
	}

	// Verify Hybrid Mode (HighSeverityMerge: "none")
	cfg.HighSeverityMerge = "none"
	resultHybrid := merger.Merge(comments, "commit123")
	if len(resultHybrid.FileComments) != 0 {
		t.Errorf("expected 0 merged file comments in hybrid mode, got %d", len(resultHybrid.FileComments))
	}
	if len(resultHybrid.NotMerged) != 3 { // Warn A, Crit A, Warn B
		t.Errorf("expected 3 not-merged comments, got %d", len(resultHybrid.NotMerged))
	}
}

func TestCommentMerger_FormatFileComment(t *testing.T) {
	cfg := &config.CommentMergeConfig{Enabled: true}
	merger := NewCommentMerger(cfg, "", createTestTemplates(), false)

	fc := &MergedFileComment{
		FilePath:  "test.go",
		Commit:    "commit123",
		ModelName: "test-model",
		Comments: []domain.ReviewComment{
			{Line: 1, Severity: "WARNING", Comment: "Test Warning"},
		},
	}

	output := merger.FormatFileComment(fc)
	// Template: INLINE: {{.Title}} {{range .TableData.Rows}}|{{index . 2}}{{end}}
	// Title: test.go Code Review
	// Row index 2: Message -> Test Warning
	expected := "INLINE: test.go Code Review |Test Warning"

	if output != expected {
		t.Errorf("format mismatch.\nExpected:\n%q\nGot:\n%q", expected, output)
	}
}

func TestCommentMerger_FormatWithLinks(t *testing.T) {
	cfg := &config.CommentMergeConfig{Enabled: true}
	// Test with WebURL
	webURL := "https://bitbucket.example.com/projects/PROJ/repos/repo/pull-requests/123"
	merger := NewCommentMerger(cfg, webURL, createTestTemplates(), false)

	// Test FormatSummaryAddons link generation
	comments := []domain.ReviewComment{
		{File: "utils.go", Line: 50, Comment: "Check this"},
	}

	output := merger.FormatSummaryAddons(comments)

	// Expect link:
	// Location: [utils.go:50](https://bitbucket.example.com/projects/PROJ/repos/repo/pull-requests/123/diff#utils.go?t=50)

	expectedLocationLink := "[utils.go:50](https://bitbucket.example.com/projects/PROJ/repos/repo/pull-requests/123/diff#utils.go?t=50)"

	// Check if output contains link text
	// Our new table has 2 columns: Location (index 0), Suggestion (index 1)
	merger.templates.Addons = template.Must(template.New("addons").Parse("{{range .TableData.Rows}}{{(index . 0)}} {{index . 1}}{{end}}"))

	output = merger.FormatSummaryAddons(comments)

	if !strings.Contains(output, expectedLocationLink) {
		t.Errorf("summary missing expected location link.\nGot: %s\nExpected: %s", output, expectedLocationLink)
	}
}
func TestCommentMerger_FormatSummaryAddons_ConfidenceColumn(t *testing.T) {
	cfg := &config.CommentMergeConfig{Enabled: true}
	// Use a dummy Web URL just for link generation
	webURL := "https://bitbucket.example.com/pr/1"

	// Case 1: showConfidence = true
	// We construct a simplified Addons template that just joins column values with pipes
	tmplWithConf := &CommentTemplates{
		Addons: template.Must(template.New("addons").Parse(
			"{{range .TableData.Rows}}{{index . 0}}|{{index . 1}}|{{index . 2}}\n{{end}}")),
	}
	mergerWithConf := NewCommentMerger(cfg, webURL, tmplWithConf, true)
	comments := []domain.ReviewComment{
		{File: "a.go", Line: 10, Confidence: 0.75, Comment: "Check this"},
	}
	output := mergerWithConf.FormatSummaryAddons(comments)

	// Expected format: Link|75%|Check this
	if !strings.Contains(output, "75%") {
		t.Errorf("expected confidence column (75%%), got: %s", output)
	}
	if !strings.Contains(output, "Check this") {
		t.Errorf("expected suggestion 'Check this', got: %s", output)
	}

	// Case 2: showConfidence = false
	tmplNoConf := &CommentTemplates{
		Addons: template.Must(template.New("addons").Parse(
			"{{range .TableData.Rows}}{{index . 0}}|{{index . 1}}\n{{end}}")),
	}
	mergerNoConf := NewCommentMerger(cfg, webURL, tmplNoConf, false)
	output2 := mergerNoConf.FormatSummaryAddons(comments)

	// Expected format: Link|Check this
	if strings.Contains(output2, "75%") {
		t.Errorf("expected no confidence column, got: %s", output2)
	}
	if !strings.Contains(output2, "Check this") {
		t.Errorf("expected suggestion 'Check this', got: %s", output2)
	}
}
