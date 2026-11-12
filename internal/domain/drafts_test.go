package domain

import (
	"testing"
	"time"
)

func TestDraftPreviewLabel(t *testing.T) {
	id := NewSiteID()
	got := DraftPreviewLabel(id)
	if got != "p-"+id || !IsDraftPreviewLabel(got) || !IsValidHostname(got) {
		t.Fatalf("label %q", got)
	}
	release := PreviewLabel(id, NewDeployID(time.Now()))
	for _, s := range []string{"p-shop", id, release, "p-" + id + "x", "P-" + id, "p-k3f9x2m7q", "p-k3f9-2m7qa"} {
		if IsDraftPreviewLabel(s) {
			t.Errorf("%q must not be a draft preview label", s)
		}
	}
	if IsPreviewLabel(got) {
		t.Error("a draft label must not look like a release preview label")
	}
}

func TestDraftPreviewLabelIsReservedSlug(t *testing.T) {
	reserved := NewReservedSlugs("example.com", "api.example.com")
	err := ValidateSlug("p-k3f9x2m7qa", reserved)
	if AsError(err).Code != CodeSlugReserved {
		t.Fatalf("draft preview label accepted as slug: %v", err)
	}
	if err := ValidateSlug("p-shop", reserved); err != nil {
		t.Fatalf("p-shop: %v", err)
	}
}
