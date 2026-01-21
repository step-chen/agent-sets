package validator

import (
	"testing"
)

func TestCommentValidator_BasicDiff(t *testing.T) {
	diff := `diff --git a/file1.go b/file1.go
index abc123..def456 100644
--- a/file1.go
+++ b/file1.go
@@ -10,6 +10,8 @@ func example() {
     existing line
     another line
+    new line 1
+    new line 2
     context line
-    removed line
     more context
`

	v := NewCommentValidator(diff)

	// Line 12 and 13 are the new lines (@ +10, then 2 context lines, then 2 new lines)
	tests := []struct {
		file  string
		line  int
		valid bool
	}{
		{"file1.go", 10, false}, // Context line
		{"file1.go", 11, false}, // Context line
		{"file1.go", 12, true},  // New line 1
		{"file1.go", 13, true},  // New line 2
		{"file1.go", 14, false}, // Context line
		{"file1.go", 15, false}, // After removed line
		{"other.go", 10, false}, // File not in diff
	}

	for _, tt := range tests {
		t.Run(tt.file+"_"+string(rune(tt.line)), func(t *testing.T) {
			got := v.IsValid(tt.file, tt.line)
			if got != tt.valid {
				t.Errorf("IsValid(%s, %d) = %v, want %v", tt.file, tt.line, got, tt.valid)
			}
		})
	}
}

func TestCommentValidator_MultipleFiles(t *testing.T) {
	diff := `diff --git a/pkg/foo.go b/pkg/foo.go
--- a/pkg/foo.go
+++ b/pkg/foo.go
@@ -1,3 +1,4 @@
 package foo
+import "fmt"
 
 func Foo() {}
diff --git a/pkg/bar.go b/pkg/bar.go
--- a/pkg/bar.go
+++ b/pkg/bar.go
@@ -5,3 +5,5 @@ func Bar() {
     x := 1
+    y := 2
+    z := 3
 }
`

	v := NewCommentValidator(diff)

	tests := []struct {
		file  string
		line  int
		valid bool
	}{
		{"pkg/foo.go", 2, true},  // import line
		{"pkg/foo.go", 1, false}, // package line (context)
		{"pkg/bar.go", 6, true},  // y := 2
		{"pkg/bar.go", 7, true},  // z := 3
		{"pkg/bar.go", 5, false}, // x := 1 (context)
	}

	for _, tt := range tests {
		got := v.IsValid(tt.file, tt.line)
		if got != tt.valid {
			t.Errorf("IsValid(%s, %d) = %v, want %v", tt.file, tt.line, got, tt.valid)
		}
	}
}

func TestCommentValidator_FileInDiff(t *testing.T) {
	diff := `diff --git a/src/main.cpp b/src/main.cpp
+++ b/src/main.cpp
@@ -1,1 +1,2 @@
 int main() {}
+// comment
`

	v := NewCommentValidator(diff)

	tests := []struct {
		file   string
		inDiff bool
	}{
		{"src/main.cpp", true},
		{"main.cpp", true}, // Partial match
		{"other.cpp", false},
	}

	for _, tt := range tests {
		got := v.FileInDiff(tt.file)
		if got != tt.inDiff {
			t.Errorf("FileInDiff(%s) = %v, want %v", tt.file, got, tt.inDiff)
		}
	}
}

func TestCommentValidator_BitbucketDiffFormat(t *testing.T) {
	// Bitbucket uses src:// and dst:// prefixes
	diff := `diff --git src://trunk/src/Common/LinkRelationMaintainer.cpp dst://trunk/src/Common/LinkRelationMaintainer.cpp
index 133182a..e232330 100644
--- src://trunk/src/Common/LinkRelationMaintainer.cpp
+++ dst://trunk/src/Common/LinkRelationMaintainer.cpp
@@ -436,6 +436,11 @@ bool LinkRelationMaintainer::processAllRelations()
             IntraRegionLaneFilterCrossMesh();
         }
 
+        if ((controlBitmask & ConvertGDBMTR::ProcessControl::LANE_TRANSITION_GENERATION))
+        {
+            LaneTransitionGenerationCrossMesh();
+        }
+
         if ((controlBitmask & ConvertGDBMTR::ProcessControl::SUPPLEMENTARY_LANE_TOPOLOGIES))
`

	v := NewCommentValidator(diff)

	tests := []struct {
		file  string
		line  int
		valid bool
	}{
		{"trunk/src/Common/LinkRelationMaintainer.cpp", 439, true},  // First + line
		{"trunk/src/Common/LinkRelationMaintainer.cpp", 440, true},  // Second + line
		{"trunk/src/Common/LinkRelationMaintainer.cpp", 441, true},  // Third + line
		{"trunk/src/Common/LinkRelationMaintainer.cpp", 442, true},  // Fourth + line
		{"trunk/src/Common/LinkRelationMaintainer.cpp", 443, true},  // Fifth + line (empty)
		{"trunk/src/Common/LinkRelationMaintainer.cpp", 436, false}, // Context line
		{"LinkRelationMaintainer.cpp", 439, true},                   // Partial match
	}

	for _, tt := range tests {
		got := v.IsValid(tt.file, tt.line)
		if got != tt.valid {
			t.Errorf("IsValid(%s, %d) = %v, want %v", tt.file, tt.line, got, tt.valid)
		}
	}
}

func TestCommentValidator_GetInvalidReason(t *testing.T) {
	diff := `diff --git a/file.go b/file.go
+++ b/file.go
@@ -10,3 +10,4 @@
 context
+new line
 more context
`

	v := NewCommentValidator(diff)

	// File not in diff
	reason := v.GetInvalidReason("other.go", 10)
	if reason != "file not in diff" {
		t.Errorf("unexpected reason: %s", reason)
	}

	// Line not in valid range
	reason = v.GetInvalidReason("file.go", 10)
	if reason == "" {
		t.Error("expected a reason for invalid line")
	}
}

func TestCommentValidator_EmptyDiff(t *testing.T) {
	v := NewCommentValidator("")

	if v.IsValid("any.go", 10) {
		t.Error("empty diff should have no valid lines")
	}

	if v.FileInDiff("any.go") {
		t.Error("empty diff should have no files")
	}
}
