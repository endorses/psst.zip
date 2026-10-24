package browser

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/source"
)

type FinalFacts struct {
	Binding              BoundFacts      `json:"builder_inputs"`
	BuilderStatic        map[string]File `json:"builder_static_files"`
	FinalStatic          map[string]File `json:"final_static_files"`
	UnresolvedJavaScript map[string]File `json:"unresolved_javascript"`
	Reproduced           bool            `json:"source_reproduction_verified"`
	Closure              bool            `json:"browser_module_closure_verified"`
	Authorized           bool            `json:"publication_authorized"`
}

// VerifyFinal reuses the runtime verifier's authenticated image bytes. Runtime
// replay verifies its own overlays and layer/config restrictions first; this
// comparison allows those exact paths and committed backend licenses only.
func VerifyFinal(bound BoundFacts, captured, git map[string][]byte, runtime source.RuntimeVerification, platform string) (FinalFacts, error) {
	result := FinalFacts{Binding: bound, BuilderStatic: map[string]File{}, FinalStatic: map[string]File{}, UnresolvedJavaScript: map[string]File{}}
	image, found := runtime.Images["web"]
	if !found || image.Platform != platform || image.Labels["org.opencontainers.image.revision"] != bound.Revision || image.Labels["org.opencontainers.image.version"] != bound.Version {
		return result, fmt.Errorf("browser image differs from bound native source")
	}
	actual := map[string][]byte{}
	for name, body := range runtime.Files["web"] {
		if strings.HasPrefix(name, "srv/web/") {
			actual[strings.TrimPrefix(name, "srv/web/")] = body
		}
	}
	built := map[string][]byte{}
	for name, body := range captured {
		if strings.HasPrefix(name, "build/") {
			built[strings.TrimPrefix(name, "build/")] = body
			result.BuilderStatic[strings.TrimPrefix(name, "build/")] = fileFact(body)
		}
	}
	if len(built) == 0 || len(actual) == 0 {
		return result, fmt.Errorf("missing actual browser static files")
	}
	allowed := map[string]File{}
	for name, body := range git {
		if strings.HasPrefix(name, "backend/licenses/") {
			target := "licenses/backend/" + strings.TrimPrefix(name, "backend/licenses/")
			allowed[target] = fileFact(body)
		}
	}
	for _, file := range runtime.NoticeFiles["web"] {
		if !strings.HasPrefix(file.Path, "srv/web/licenses/runtime/") && file.Path != "srv/web/licenses/release.json" {
			return result, fmt.Errorf("unexpected runtime browser overlay path")
		}
		allowed[strings.TrimPrefix(file.Path, "srv/web/")] = File{file.SHA256, file.Size}
	}
	for name, want := range allowed {
		body, found := actual[name]
		if !found || fileFact(body) != want {
			return result, fmt.Errorf("final browser legal metadata differs from verified sources")
		}
	}
	for name, body := range built {
		got, found := actual[name]
		if !found {
			return result, fmt.Errorf("final browser output missing")
		}
		if _, overlay := allowed[name]; overlay && name == "licenses/release.json" {
			continue
		}
		if !bytes.Equal(got, body) {
			return result, fmt.Errorf("final browser bytes differ from actual builder")
		}
	}
	for name, body := range actual {
		result.FinalStatic[name] = fileFact(body)
		if _, found := built[name]; !found {
			if _, allowed := allowed[name]; !allowed {
				return result, fmt.Errorf("final browser image introduces an unrelated static file")
			}
		}
	}
	for name, file := range bound.Unattributed {
		if bound.CopiedStatic[name] != file {
			result.UnresolvedJavaScript[name] = file
		}
	}
	return result, nil
}
