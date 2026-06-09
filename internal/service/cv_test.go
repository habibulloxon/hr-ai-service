package service

import "testing"

func TestParseByFileType_Txt(t *testing.T) {
	content := []byte("Jane Doe\nSoftware Engineer\n")
	got, err := parseByFileType(content, ".txt")
	if err != nil {
		t.Fatalf("parseByFileType: %v", err)
	}
	if got != string(content) {
		t.Errorf("got %q, want %q", got, string(content))
	}
}

func TestCleanCVText(t *testing.T) {
	in := "  Jane   Doe  \n\n\n  Engineer \n   "
	want := "Jane Doe\nEngineer"
	if got := cleanCVText(in); got != want {
		t.Errorf("cleanCVText = %q, want %q", got, want)
	}
}
