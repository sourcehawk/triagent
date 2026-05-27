package watches

import "testing"

func TestFormatWatchTitle(t *testing.T) {
	got := FormatWatchTitle("c1 triage", "Engine OOMs after upgrade. Cluster prod-emea-1 reported repeatedly.")
	want := "[watch: c1 triage] Engine OOMs after upgrade."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFormatWatchTitleTruncatesWatchName(t *testing.T) {
	long := "this watch name is more than thirty-two characters wide"
	got := FormatWatchTitle(long, "hello world")
	if len(got) > 32+len("[watch: ] hello world")+10 {
		t.Fatalf("title too long: %q", got)
	}
}

func TestFormatWatchTitleFallback(t *testing.T) {
	got := FormatWatchTitle("n", "")
	if got != "[watch: n] untitled" {
		t.Fatalf("got %q", got)
	}
}
