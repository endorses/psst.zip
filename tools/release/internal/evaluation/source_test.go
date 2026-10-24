package evaluation

import (
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/browser"
)

func TestBrowserGateUsesIndependentPreferredFactsWithoutRewritingMeasurement(t *testing.T) {
	measured := browser.NativeFacts{BuilderObservedInputsVerified: true, GitSourceBindingVerified: true, NPMMemberIntegrityVerified: true, OCIImageVerified: true, PreferredSourceComplete: false}
	preferred := browser.PreferredFacts{Catalog: browser.File{SHA256: "sha256:" + strings.Repeat("a", 64), Size: 100}, Modules: map[string]browser.PreferredRelationship{"observed-module": {Method: "direct"}}, Generators: map[string]browser.PreferredRelationship{"generator": {Method: "generated"}}}
	if err := checkBrowserSource(measured, preferred); err != nil {
		t.Fatal(err)
	}
	if measured.PreferredSourceComplete {
		t.Fatal("measurement was changed into an approval")
	}
	preferred.Catalog.SHA256 = ""
	if err := checkBrowserSource(measured, preferred); err == nil {
		t.Fatal("accepted missing independent preferred-source catalog")
	}
	preferred.Catalog.SHA256 = "sha256:" + strings.Repeat("a", 64)
	measured.OCIImageVerified = false
	if err := checkBrowserSource(measured, preferred); err == nil {
		t.Fatal("accepted incomplete actual final-byte replay")
	}
}
