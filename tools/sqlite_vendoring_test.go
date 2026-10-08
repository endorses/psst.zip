package main

import (
	"strings"
	"testing"
)

func sqliteVendoringFixture() (string, string) {
	original := "//go:build linux && amd64\n\npackage libsqlite3\nimport \"fmt\"\nconst SQLITE_TRANSIENT = -1\nconst KEEP = \"line\\nvalue\"\n"
	target := "//go:build linux && amd64\n\npackage sqlite3\nimport \"fmt\"\nconst KEEP = \"line\\nvalue\"\n"
	for _, name := range finalSQLiteAliases {
		original += "type T" + name + " = struct{}\n"
		target += "type T" + name + " = struct{}\ntype " + name + " = T" + name + "\n"
	}
	// The original already owns thing, so the stripped alias is not added.
	original += "type Tthing = int\ntype thing = int\n"
	target += "type Tthing = int\ntype thing = int\n"
	body := "var n = 3\nvar directiveText = \"//go:norace\\n//go:linkname symbol\"\nfunc consume(xs ...int) { fmt.Println(xs) }\nfunc call(xs ...int) int { consume(xs...); return 7 }\n"
	original += body
	target += body
	for _, name := range finalSQLiteAliases {
		target += "type S" + name[1:] + " = " + name + "\n"
	}
	return original, target
}

func TestSQLiteVendoringRecipeAndFormatting(t *testing.T) {
	original, target := sqliteVendoringFixture()
	target = "// comment before package\n" + strings.ReplaceAll(target, "type ", "/* retained comment */ type ")
	result, err := compareSQLiteVendoring([]byte(original), []byte(target))
	if err != nil {
		t.Fatal(err)
	}
	if result.AddedAliasCount != 14 || result.ExpectedStructuralSHA256 != result.TargetStructuralSHA256 || result.GeneratedOutputReproductionVerified {
		t.Fatalf("incorrect correspondence result: %+v", result)
	}
	if result.OriginalSHA256 == result.VendoredSHA256 {
		t.Fatal("source byte identities were lost")
	}
}

func TestSQLiteVendoringRejectsSemanticAndRecipeSubstitutions(t *testing.T) {
	original, target := sqliteVendoringFixture()
	mutations := []struct{ name, old, replacement string }{
		{"body", "return 7", "return 8"},
		{"alias", "type sqlite3_int64 = Tsqlite3_int64", "type sqlite3_int64 = int64"},
		{"defined-type", "type sqlite3_int64 = Tsqlite3_int64", "type sqlite3_int64 Tsqlite3_int64"},
		{"literal", `"line\nvalue"`, `"line value"`},
		{"semicolon", "return 7", "return\n7"},
		{"variadic-call", "consume(xs...)", "consume(xs)"},
		{"variadic-parameter", "func consume(xs ...int)", "func consume(xs []int)"},
		{"import", `"fmt"`, `"os"`},
		{"syntax", "return 7", "return ("},
		{"changed-build-tag", "//go:build linux && amd64", "//go:build linux && arm64"},
		{"moved-build-tag", "//go:build linux && amd64\n\npackage sqlite3", "package sqlite3\n//go:build linux && amd64"},
		{"norace", "func call", "//go:norace\nfunc call"},
		{"linkname", "func call", "//go:linkname call other\nfunc call"},
		{"line", "func call", "//line substituted.go:1\nfunc call"},
		{"block-line", "func call", "/*line substituted.go:1*/\nfunc call"},
		{"export", "func call", "//export call\nfunc call"},
	}
	for _, change := range mutations {
		t.Run(change.name, func(t *testing.T) {
			modified := strings.Replace(target, change.old, change.replacement, 1)
			if modified == target {
				t.Fatal("mutation did not change fixture")
			}
			if _, err := compareSQLiteVendoring([]byte(original), []byte(modified)); err == nil {
				t.Fatal("accepted changed semantics")
			}
		})
	}
	for _, change := range []struct{ name, value string }{
		{"missing-transient", strings.Replace(original, "const SQLITE_TRANSIENT = -1\n", "", 1)},
		{"duplicate-transient", original + "const SQLITE_TRANSIENT = 0\n"},
		{"grouped-type", strings.Replace(original, "type Tthing = int", "type (Tthing = int)", 1)},
		{"original-defined-type", strings.Replace(original, "type Tthing = int", "type Tthing int", 1)},
		{"package", strings.Replace(original, "libsqlite3", "other", 1)},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := compareSQLiteVendoring([]byte(change.value), []byte(target)); err == nil {
				t.Fatal("accepted unsupported recipe shape")
			}
		})
	}
}
