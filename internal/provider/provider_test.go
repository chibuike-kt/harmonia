package provider

import (
	"strings"
	"testing"
)

func TestApplyCitations_NoCitations(t *testing.T) {
	content := "plain reply, no search"
	got := ApplyCitations(content, nil)
	if got != content {
		t.Errorf("ApplyCitations with no citations = %q, want unchanged %q", got, content)
	}
}

func TestApplyCitations_InsertsMarkerAndSourcesList(t *testing.T) {
	content := "The sky is blue because of Rayleigh scattering."
	citations := []Citation{
		{Title: "Why Is the Sky Blue", URL: "https://example.com/sky", AfterText: "Rayleigh scattering."},
	}

	got := ApplyCitations(content, citations)

	want := "The sky is blue because of Rayleigh scattering.[1]\n\n**Sources**\n1. [Why Is the Sky Blue](https://example.com/sky)\n"
	if got != want {
		t.Errorf("ApplyCitations = %q, want %q", got, want)
	}
}

func TestApplyCitations_DedupesRepeatedURL(t *testing.T) {
	content := "First fact. Second fact."
	citations := []Citation{
		{Title: "Source", URL: "https://example.com/x", AfterText: "First fact."},
		{Title: "Source", URL: "https://example.com/x", AfterText: "Second fact."},
	}

	got := ApplyCitations(content, citations)

	// Both citations share a URL, so both inline occurrences get the
	// same marker number, and the URL appears in the Sources list once
	// — not two separately numbered sources.
	if strings.Count(got, "[1]") != 2 {
		t.Errorf("occurrences of \"[1]\" = %d, want 2 (one inline marker per citation, both numbered 1)", strings.Count(got, "[1]"))
	}
	if strings.Count(got, "https://example.com/x") != 1 {
		t.Errorf("occurrences of the shared URL = %d, want 1 (deduped Sources entry)", strings.Count(got, "https://example.com/x"))
	}
}

func TestApplyCitations_SkipsAfterTextNotFoundInContent(t *testing.T) {
	content := "A reply with no matching quote."
	citations := []Citation{
		{Title: "Ghost", URL: "https://example.com/ghost", AfterText: "text that never appears"},
	}

	got := ApplyCitations(content, citations)
	if got != content {
		t.Errorf("ApplyCitations with an unmatched AfterText = %q, want content left unchanged: %q", got, content)
	}
}
