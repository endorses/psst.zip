// sqlite_vendoring verifies the retained SQLite vendoring recipe with Go syntax.
// It never executes the upstream generator or claims generated C reproduction.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
)

const maxSQLiteSource = 40 << 20

var finalSQLiteAliases = []string{
	"sqlite3_int64", "sqlite3_mutex_methods", "sqlite3_value", "sqlite3_index_info",
	"sqlite3_module", "sqlite3_vtab", "sqlite3_vtab_cursor",
}

type vendoringResult struct {
	Kind                                string `json:"kind"`
	ExpectedStructuralSHA256            string `json:"expected_structural_sha256"`
	TargetStructuralSHA256              string `json:"target_structural_sha256"`
	OriginalSHA256                      string `json:"original_sha256"`
	VendoredSHA256                      string `json:"vendored_sha256"`
	AddedAliasCount                     int    `json:"added_alias_count"`
	GeneratedOutputReproductionVerified bool   `json:"generated_output_reproduction_verified"`
}

func sqliteDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

func readSQLiteSource(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > maxSQLiteSource {
		return nil, fmt.Errorf("source must be a regular file no larger than 40 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxSQLiteSource+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSQLiteSource {
		return nil, fmt.Errorf("source exceeded 40 MiB while reading")
	}
	return raw, nil
}

func sqliteAlias(name, original string) ast.Decl {
	return &ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{Name: ast.NewIdent(name), Assign: token.Pos(1), Type: ast.NewIdent(original)}}}
}

func transformSQLite(file *ast.File) (int, error) {
	if file.Name.Name != "libsqlite3" {
		return 0, fmt.Errorf("original package is not libsqlite3")
	}
	taken := make(map[string]bool)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		if len(gen.Specs) != 1 || gen.Lparen.IsValid() {
			return 0, fmt.Errorf("unsupported grouped original type declaration")
		}
		spec, ok := gen.Specs[0].(*ast.TypeSpec)
		if !ok || !spec.Assign.IsValid() || spec.TypeParams != nil || taken[spec.Name.Name] {
			return 0, fmt.Errorf("original types must be unique single aliases")
		}
		taken[spec.Name.Name] = true
	}
	var declarations []ast.Decl
	removed, added := 0, 0
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil {
				return 0, fmt.Errorf("unsupported original method declaration")
			}
			declarations = append(declarations, d)
		case *ast.GenDecl:
			switch d.Tok {
			case token.IMPORT, token.VAR:
				declarations = append(declarations, d)
			case token.CONST:
				if len(d.Specs) != 1 || d.Lparen.IsValid() {
					return 0, fmt.Errorf("unsupported original const declaration")
				}
				spec, ok := d.Specs[0].(*ast.ValueSpec)
				if !ok || len(spec.Names) != 1 {
					return 0, fmt.Errorf("original const must have one name")
				}
				if spec.Names[0].Name == "SQLITE_TRANSIENT" {
					removed++
					continue
				}
				declarations = append(declarations, d)
			case token.TYPE:
				declarations = append(declarations, d)
				name := d.Specs[0].(*ast.TypeSpec).Name.Name
				if token.IsExported(name) {
					alias := name[1:]
					if alias == "" {
						return 0, fmt.Errorf("empty stripped type alias")
					}
					if !taken[alias] {
						declarations = append(declarations, sqliteAlias(alias, name))
						added++
					}
				}
			default:
				return 0, fmt.Errorf("unsupported original declaration token")
			}
		default:
			return 0, fmt.Errorf("unsupported original declaration")
		}
	}
	if removed != 1 {
		return 0, fmt.Errorf("SQLITE_TRANSIENT must occur exactly once")
	}
	for _, original := range finalSQLiteAliases {
		declarations = append(declarations, sqliteAlias("S"+original[1:], original))
		added++
	}
	file.Name = ast.NewIdent("sqlite3")
	file.Decls = declarations
	return added, nil
}

var sqlitePositionType = reflect.TypeOf(token.Pos(0))

// Positions/comments do not affect this comparison. Assign and Ellipsis are
// special: their position validity encodes alias and variadic-call semantics.
// Node types, field names, declaration order, literal spellings and every other
// non-position field are preserved. Parser object/scope metadata is excluded.
func sqliteWriteStructure(value reflect.Value, writer *bufio.Writer) error {
	write := func(s string) { _, _ = writer.WriteString(s) }
	if !value.IsValid() {
		write("null")
		return nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			write("null")
			return nil
		}
		return sqliteWriteStructure(value.Elem(), writer)
	}
	switch value.Kind() {
	case reflect.Struct:
		write("[" + strconv.Quote(value.Type().String()))
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.Type == sqlitePositionType {
				if field.Name == "Assign" || field.Name == "Ellipsis" {
					write(",[" + strconv.Quote(field.Name) + "," + strconv.FormatBool(value.Field(i).Int() != 0) + "]")
				}
				continue
			}
			switch field.Name {
			case "Doc", "Comment", "Comments", "Obj", "Scope", "Unresolved", "Imports", "GoVersion":
				continue
			}
			write(",[" + strconv.Quote(field.Name) + ",")
			if err := sqliteWriteStructure(value.Field(i), writer); err != nil {
				return err
			}
			write("]")
		}
		write("]")
	case reflect.Slice:
		if value.IsNil() {
			write("null")
			return nil
		}
		write("[")
		for i := 0; i < value.Len(); i++ {
			if i != 0 {
				write(",")
			}
			if err := sqliteWriteStructure(value.Index(i), writer); err != nil {
				return err
			}
		}
		write("]")
	case reflect.String:
		write(strconv.Quote(value.String()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		write(strconv.FormatInt(value.Int(), 10))
	case reflect.Bool:
		write(strconv.FormatBool(value.Bool()))
	default:
		return fmt.Errorf("unsupported syntax field type %s", value.Type())
	}
	return nil
}

func sqliteBuildConstraint(file *ast.File) (string, error) {
	build := ""
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := comment.Text
			if strings.HasPrefix(text, "//go:build") {
				if build != "" || comment.End() >= file.Package {
					return "", fmt.Errorf("build constraint must occur once before package")
				}
				if text != "//go:build linux && amd64" && text != "//go:build linux && arm64" {
					return "", fmt.Errorf("unsupported retained SQLite build constraint")
				}
				build = text
				continue
			}
			if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "//line") || strings.HasPrefix(text, "/*line") || strings.HasPrefix(text, "//export") || strings.HasPrefix(text, "// +build") {
				return "", fmt.Errorf("unsupported compiler directive comment")
			}
		}
	}
	if build == "" {
		return "", fmt.Errorf("missing retained SQLite build constraint")
	}
	return build, nil
}

func sqliteStructuralDigest(file *ast.File, build string) (string, error) {
	h := sha256.New()
	writer := bufio.NewWriterSize(h, 64<<10)
	// Compiler build constraints are semantic comments and precede the AST hash.
	_, _ = writer.WriteString(strconv.Quote(build) + "\n")
	if err := sqliteWriteStructure(reflect.ValueOf(file), writer); err != nil {
		return "", err
	}
	if err := writer.Flush(); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func compareSQLiteVendoring(original, vendored []byte) (vendoringResult, error) {
	var result vendoringResult
	if len(original) > maxSQLiteSource || len(vendored) > maxSQLiteSource {
		return result, fmt.Errorf("source exceeds 40 MiB")
	}
	origin, err := parser.ParseFile(token.NewFileSet(), "original.go", original, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return result, fmt.Errorf("original syntax: %w", err)
	}
	target, err := parser.ParseFile(token.NewFileSet(), "vendored.go", vendored, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return result, fmt.Errorf("vendored syntax: %w", err)
	}
	originalBuild, err := sqliteBuildConstraint(origin)
	if err != nil {
		return result, fmt.Errorf("original directives: %w", err)
	}
	targetBuild, err := sqliteBuildConstraint(target)
	if err != nil {
		return result, fmt.Errorf("vendored directives: %w", err)
	}
	if originalBuild != targetBuild {
		return result, fmt.Errorf("vendored build constraint differs")
	}
	added, err := transformSQLite(origin)
	if err != nil {
		return result, err
	}
	expected, err := sqliteStructuralDigest(origin, originalBuild)
	if err != nil {
		return result, err
	}
	actual, err := sqliteStructuralDigest(target, targetBuild)
	if err != nil {
		return result, err
	}
	if expected != actual {
		return result, fmt.Errorf("vendored Go syntax differs from retained recipe transformation")
	}
	return vendoringResult{Kind: "sqlite-vendoring-go-ast-correspondence", ExpectedStructuralSHA256: expected, TargetStructuralSHA256: actual, OriginalSHA256: sqliteDigest(original), VendoredSHA256: sqliteDigest(vendored), AddedAliasCount: added}, nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sqlite_vendoring ORIGINAL_GO VENDORED_GO")
		os.Exit(2)
	}
	original, err := readSQLiteSource(os.Args[1])
	if err == nil {
		var vendored []byte
		vendored, err = readSQLiteSource(os.Args[2])
		if err == nil {
			var result vendoringResult
			result, err = compareSQLiteVendoring(original, vendored)
			if err == nil {
				err = json.NewEncoder(os.Stdout).Encode(result)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
