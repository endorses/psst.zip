package source

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

const runtimeLegalRoot = "tools/runtime-legal/"

var scopedOrigins = map[string]bool{"alpine-base": true, "alpine-baselayout": true, "alpine-keys": true, "ca-certificates": true}

type legalReviewDocument struct {
	Path        string
	Size        int64
	SHA256      string
	GitBlobSHA1 string `json:"git_blob_sha1"`
	LicenseID   string `json:"license_id"`
}
type legalReviewFile struct {
	Name   string
	Size   int64
	SHA256 string
}
type legalReviewOrigin struct {
	Origin, Version string
	AportsCommit    string            `json:"aports_commit"`
	DeclaredLicense string            `json:"declared_license"`
	RecipePath      string            `json:"recipe_path"`
	RecipeFiles     []legalReviewFile `json:"recipe_files"`
	NoticePaths     []string          `json:"notice_paths"`
	ExternalInputs  []struct {
		Name, SHA512, SHA256 string
		Members              []struct {
			Name         string
			Size         int64
			SHA256       string
			NoticePath   string `json:"notice_path"`
			NoticeStart  int    `json:"notice_start"`
			NoticeLength int    `json:"notice_length"`
		}
	} `json:"external_inputs"`
	ReviewRequired  bool     `json:"review_required"`
	RemainingReview []string `json:"remaining_review"`
}
type runtimeReview struct {
	SchemaVersion  int      `json:"schema_version"`
	ReviewRequired bool     `json:"review_required"`
	Remaining      []string `json:"remaining_distribution_gates"`
	Documents      []legalReviewDocument
	Origins        []legalReviewOrigin
	BaseImages     []struct {
		Architecture string
		BaseRef      string `json:"base_ref"`
		Packages     []APK
	} `json:"base_images"`
}
type reviewedLegal struct {
	catalog   runtimeReview
	documents map[string][]byte
	standards map[string][]byte
	origins   map[string]legalReviewOrigin
	files     map[string][]byte
}

func reviewOriginKey(origin, version, commit string) string {
	return origin + "/" + version + "/" + commit
}
func runtimeLegal(ctx context.Context, root string, source Context) (reviewedLegal, error) {
	result := reviewedLegal{documents: map[string][]byte{}, standards: map[string][]byte{}, origins: map[string]legalReviewOrigin{}, files: map[string][]byte{}}
	raw, err := committed(ctx, root, source.Commit, runtimeLegalRoot+"review.json")
	if err != nil {
		return result, err
	}
	if _, err = jsonObject(raw); err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &result.catalog) != nil || result.catalog.SchemaVersion != 1 || !result.catalog.ReviewRequired || len(result.catalog.Remaining) == 0 {
		return result, fmt.Errorf("unsupported exact runtime legal catalog")
	}
	result.files["legal-review/review.json"] = raw
	for _, doc := range result.catalog.Documents {
		if !safePath(doc.Path) || doc.Size < 0 || doc.Size > 16<<20 || !digestRE.MatchString("sha256:"+doc.SHA256) || result.documents[doc.Path] != nil {
			return result, fmt.Errorf("invalid or duplicate runtime legal reference")
		}
		body, e := committed(ctx, root, source.Commit, runtimeLegalRoot+doc.Path)
		if e != nil {
			return result, e
		}
		if int64(len(body)) != doc.Size || hash(body) != "sha256:"+doc.SHA256 || doc.GitBlobSHA1 != "" && gitBlob(body) != doc.GitBlobSHA1 {
			return result, fmt.Errorf("pinned runtime legal evidence bytes differ")
		}
		result.documents[doc.Path] = body
		result.files["legal-review/"+doc.Path] = body
		if doc.LicenseID != "" {
			if _, ok := result.standards[doc.LicenseID]; ok {
				return result, fmt.Errorf("duplicate standard runtime license")
			}
			result.standards[doc.LicenseID] = body
		}
	}
	scopes := map[string]bool{}
	for _, origin := range result.catalog.Origins {
		key := reviewOriginKey(origin.Origin, origin.Version, origin.AportsCommit)
		if !scopedOrigins[origin.Origin] || !commitRE.MatchString(origin.AportsCommit) || !origin.ReviewRequired || len(origin.RemainingReview) == 0 {
			return result, fmt.Errorf("invalid reviewed runtime origin")
		}
		if _, ok := result.origins[key]; ok {
			return result, fmt.Errorf("duplicate reviewed runtime origin")
		}
		if result.documents[origin.RecipePath] == nil {
			return result, fmt.Errorf("reviewed runtime recipe missing")
		}
		for _, name := range origin.NoticePaths {
			if result.documents[name] == nil {
				return result, fmt.Errorf("reviewed runtime notice missing")
			}
		}
		result.origins[key] = origin
		scopes[origin.Origin] = true
	}
	if len(scopes) != len(scopedOrigins) {
		return result, fmt.Errorf("reviewed runtime origin coverage differs")
	}
	for _, image := range result.catalog.BaseImages {
		if image.Architecture != "amd64" || !strings.Contains(image.BaseRef, "@sha256:") {
			return result, fmt.Errorf("invalid reviewed base image")
		}
		for _, p := range image.Packages {
			origin, ok := result.origins[apkOrigin(p)]
			if !ok || origin.DeclaredLicense != p.License {
				return result, fmt.Errorf("reviewed base source/license differs")
			}
		}
	}
	return result, nil
}
func verifyScopedRuntime(origin Source, packages []APK, files map[string][]byte, review reviewedLegal) error {
	if !scopedOrigins[origin.Name] {
		return nil
	}
	if origin.Recipe == nil {
		return fmt.Errorf("runtime recipe metadata missing")
	}
	key := reviewOriginKey(origin.Name, origin.Version, origin.Recipe.Commit)
	reviewed, ok := review.origins[key]
	if !ok {
		return fmt.Errorf("runtime metadata origin has no exact reviewed treatment")
	}
	recipeRoot := "recipes/" + origin.ID + "/"
	if !bytes.Equal(files[recipeRoot+"APKBUILD"], review.documents[reviewed.RecipePath]) {
		return fmt.Errorf("runtime recipe differs from reviewed original")
	}
	for _, expected := range reviewed.RecipeFiles {
		body := files[recipeRoot+expected.Name]
		if int64(len(body)) != expected.Size || hash(body) != "sha256:"+expected.SHA256 {
			return fmt.Errorf("runtime recipe helper differs from reviewed original")
		}
	}
	for _, p := range packages {
		if apkOrigin(p) == key && p.License != reviewed.DeclaredLicense {
			return fmt.Errorf("runtime license differs from reviewed source declaration")
		}
	}
	raw := files[origin.Original.Path]
	for _, expected := range reviewed.ExternalInputs {
		selected, err := originalTarFiles(raw, func(name string) bool { return path.Base(name) == expected.Name })
		if err != nil {
			return err
		}
		body, err := soleSuffix(selected, expected.Name, false)
		if err != nil {
			return err
		}
		sum := sha512.Sum512(body)
		if hash(body) != "sha256:"+expected.SHA256 || hex.EncodeToString(sum[:]) != expected.SHA512 {
			return fmt.Errorf("runtime original input differs from reviewed source recipe")
		}
		for _, member := range expected.Members {
			selected, err := originalTarFiles(body, func(name string) bool { return name == member.Name })
			if err != nil {
				return err
			}
			content := selected[member.Name]
			if int64(len(content)) != member.Size || hash(content) != "sha256:"+member.SHA256 || member.NoticeStart < 0 || member.NoticeLength < 0 || member.NoticeStart+member.NoticeLength > len(content) || !bytes.Equal(content[member.NoticeStart:member.NoticeStart+member.NoticeLength], review.documents[member.NoticePath]) {
				return fmt.Errorf("runtime embedded original legal notice differs from reviewed source")
			}
		}
	}
	return nil
}

var licenseTokenRE = regexp.MustCompile(`[A-Za-z0-9.+-]+|[()]`)

func runtimeLicenseIDs(expression string, standards map[string][]byte) ([]string, error) {
	if expression == "Public-Domain" || expression == "Public Domain" {
		return []string{}, nil
	}
	tokens := licenseTokenRE.FindAllString(expression, -1)
	compact := strings.Join(strings.Fields(expression), "")
	if strings.Join(tokens, "") != compact {
		return nil, fmt.Errorf("unsupported runtime license expression")
	}
	position := 0
	ids := map[string]bool{}
	var expressionGroup func() error
	var atom func() error
	atom = func() error {
		if position >= len(tokens) {
			return fmt.Errorf("incomplete runtime license expression")
		}
		token := tokens[position]
		position++
		if token == "(" {
			if err := expressionGroup(); err != nil {
				return err
			}
			if position >= len(tokens) || tokens[position] != ")" {
				return fmt.Errorf("unbalanced runtime license expression")
			}
			position++
			return nil
		}
		if standards[token] == nil {
			return fmt.Errorf("runtime license terms need reviewed standard reference")
		}
		ids[token] = true
		return nil
	}
	expressionGroup = func() error {
		if err := atom(); err != nil {
			return err
		}
		for position < len(tokens) && (tokens[position] == "AND" || tokens[position] == "OR") {
			position++
			if err := atom(); err != nil {
				return err
			}
		}
		return nil
	}
	if err := expressionGroup(); err != nil {
		return nil, err
	}
	if position != len(tokens) {
		return nil, fmt.Errorf("unsupported runtime license syntax")
	}
	var result []string
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
func runtimeNotices(graph RuntimeGraph, sources map[string]Source, files map[string][]byte, review reviewedLegal) ([]byte, error) {
	var output bytes.Buffer
	output.WriteString("psst.zip exact runtime notices and source references\n")
	origins := map[string][]APK{}
	for _, p := range graph.Packages {
		origins[apkOrigin(p)] = append(origins[apkOrigin(p)], p)
	}
	var keys []string
	for key := range origins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	supplements := map[string]bool{}
	for _, key := range keys {
		packages := origins[key]
		id := "apk-" + strings.TrimPrefix(hash([]byte(key)), "sha256:")
		source, ok := sources[id]
		if !ok || source.Recipe == nil {
			return nil, fmt.Errorf("retained runtime origin lacks source original")
		}
		declared := map[string]bool{}
		for _, name := range source.Recipe.DeclaredPackages {
			declared[name] = true
		}
		licenseExpressions := map[string]bool{}
		standards := map[string]bool{}
		for _, p := range packages {
			if !declared[p.Name] {
				return nil, fmt.Errorf("undeclared runtime package")
			}
			licenseExpressions[p.License] = true
			ids, err := runtimeLicenseIDs(p.License, review.standards)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				standards[id] = true
			}
		}
		if len(source.NoticeFiles) == 0 && !scopedOrigins[source.Name] {
			return nil, fmt.Errorf("runtime original supplies no legal evidence and has no reviewed treatment")
		}
		if err := verifyScopedRuntime(source, graph.Packages, files, review); err != nil {
			return nil, err
		}
		if !regexp.MustCompile(`(?m)^license=["']([^"']+)["']$`).Match(files["recipes/"+id+"/APKBUILD"]) {
			return nil, fmt.Errorf("runtime recipe license declaration missing")
		}
		var expressions []string
		for expression := range licenseExpressions {
			expressions = append(expressions, expression)
		}
		sort.Strings(expressions)
		fmt.Fprintf(&output, "\n%s %s\nPackaging commit: %s\nDeclared licenses: %s\n", source.Name, source.Version, source.Recipe.Commit, strings.Join(expressions, "; "))
		appendSourceNotices(&output, source, files)
		var terms []string
		for id := range standards {
			terms = append(terms, id)
		}
		sort.Strings(terms)
		for _, id := range terms {
			fmt.Fprintf(&output, "\nFull %s terms (standard reference; original attributions above or in retained source)\n", id)
			output.Write(review.standards[id])
			output.WriteByte('\n')
		}
		if treatment, ok := review.origins[key]; ok {
			for _, name := range treatment.NoticePaths {
				supplements[name] = true
			}
		}
	}
	var names []string
	for name := range supplements {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&output, "\nSupplemental exact-source notice / license reference: %s\n", name)
		output.Write(review.documents[name])
		output.WriteByte('\n')
	}
	output.WriteString("\nAlpine release metadata and public signing-key notice treatment: preserve the upstream MIT declaration, full MIT terms and every supplied notice/recipe/source input. No copyright owner was supplied for these data origins; none is invented here. Standard template placeholders do not attribute ownership.\n")
	return output.Bytes(), nil
}
func appendSourceNotices(out *bytes.Buffer, source Source, files map[string][]byte) {
	var labels []string
	for label := range source.NoticeFiles {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		fmt.Fprintf(out, "\n%s\n", label)
		out.Write(files[source.NoticeFiles[label].Path])
		out.WriteByte('\n')
	}
}
