package exohub

import "testing"

func TestIsReference(t *testing.T) {
	tests := map[string]bool{
		"exohub:project@main":          true,
		"exohub:project:path/file@v1":  true,
		"https://example.com/file.csv": false,
		"./local/path":                 false,
		"exohub":                       false,
	}
	for s, want := range tests {
		if got := IsReference(s); got != want {
			t.Errorf("IsReference(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParseReferenceAsset(t *testing.T) {
	ref, err := ParseReference("exohub:rnaseq-yeast-gse110004-fastq-mini:fastq/SRR6357070_1.fastq.gz@main")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if ref.Project != "rnaseq-yeast-gse110004-fastq-mini" {
		t.Errorf("Project = %q", ref.Project)
	}
	if ref.Version != "main" {
		t.Errorf("Version = %q", ref.Version)
	}
	if ref.Path != "fastq/SRR6357070_1.fastq.gz" {
		t.Errorf("Path = %q", ref.Path)
	}
	if ref.IsFolder() || ref.IsCollection() || ref.IsProject() {
		t.Errorf("expected an asset reference, got %+v", ref)
	}
	if !ref.IsAsset() {
		t.Error("IsAsset() = false, want true")
	}
	wantID := "rnaseq-yeast-gse110004-fastq-mini:fastq/SRR6357070_1.fastq.gz@main"
	if got := ref.CatalogID(); got != wantID {
		t.Errorf("CatalogID() = %q, want %q", got, wantID)
	}
}

func TestParseReferenceFolder(t *testing.T) {
	ref, err := ParseReference("exohub:myproject:data/fastq/@v2")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if !ref.IsFolder() {
		t.Error("IsFolder() = false, want true")
	}
	if ref.IsAsset() || ref.IsCollection() || ref.IsProject() {
		t.Errorf("expected a folder reference, got %+v", ref)
	}
}

func TestParseReferenceWholeProject(t *testing.T) {
	ref, err := ParseReference("exohub:other-project@v2")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if !ref.IsProject() {
		t.Error("IsProject() = false, want true")
	}
	if ref.Path != "" || ref.CollectionName != "" {
		t.Errorf("expected no path/collection, got %+v", ref)
	}
	if got, want := ref.CatalogID(), "other-project@v2"; got != want {
		t.Errorf("CatalogID() = %q, want %q", got, want)
	}
}

func TestParseReferenceCollection(t *testing.T) {
	ref, err := ParseReference("exohub:cohort-project:collection:cohort-a@v1")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if !ref.IsCollection() {
		t.Error("IsCollection() = false, want true")
	}
	if ref.CollectionName != "cohort-a" {
		t.Errorf("CollectionName = %q", ref.CollectionName)
	}
	if ref.Path != "" {
		t.Errorf("Path = %q, want empty", ref.Path)
	}
}

func TestParseReferenceRejectsMissingVersion(t *testing.T) {
	_, err := ParseReference("exohub:myproject:path/file.csv")
	if err == nil {
		t.Fatal("expected an error for a missing @<version>")
	}
}

func TestParseReferenceRejectsMissingProject(t *testing.T) {
	for _, s := range []string{"exohub:@main", "exohub:"} {
		if _, err := ParseReference(s); err == nil {
			t.Errorf("ParseReference(%q) expected an error", s)
		}
	}
}

func TestParseReferenceRejectsEmptyPath(t *testing.T) {
	if _, err := ParseReference("exohub:myproject:@main"); err == nil {
		t.Error("expected an error for an empty path after the project")
	}
}

func TestParseReferenceRejectsEmptyCollectionName(t *testing.T) {
	if _, err := ParseReference("exohub:myproject:collection:@main"); err == nil {
		t.Error("expected an error for an empty collection name")
	}
}

func TestParseReferenceRejectsNonExohubScheme(t *testing.T) {
	if _, err := ParseReference("https://example.com/file@v1"); err == nil {
		t.Error("expected an error for a non-exohub reference")
	}
}

func TestParseReferenceAtInPathBase(t *testing.T) {
	// A file whose name itself contains "@" is resolved using the last "@"
	// as the version separator.
	ref, err := ParseReference("exohub:myproject:data/file@2x.png@v1")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if ref.Version != "v1" {
		t.Errorf("Version = %q, want v1", ref.Version)
	}
	if ref.Path != "data/file@2x.png" {
		t.Errorf("Path = %q", ref.Path)
	}
}
