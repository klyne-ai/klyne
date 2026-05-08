// dump-contracts is a small Go AST tool that emits a JSON dump of every
// exported struct (and its fields + JSON tags) declared in klyne's
// W0-frozen contract files. The frontend's contract-check (W13) loads
// this dump and asserts its TypeScript DTOs match field-by-field.
//
// Usage:
//
//	dump-contracts                # writes JSON to stdout
//	dump-contracts -out file.json # writes JSON to file.json
//	dump-contracts -files a.go,b.go # override the default file list
//
// The default file list is the three W0-frozen contract files:
//
//	internal/api/contracts.go
//	internal/api/sse_events.go
//	internal/connectors/connector.go
//
// Output shape (JSON):
//
//	{
//	  "structs": [
//	    {
//	      "name": "Message",
//	      "package": "connectors",
//	      "file": "internal/connectors/connector.go",
//	      "fields": [
//	        {"name": "ID", "type": "string", "json": "id"},
//	        ...
//	      ]
//	    },
//	    ...
//	  ]
//	}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
)

// Field is one struct field + its JSON tag (or "" if absent).
type Field struct {
	Name string `json:"name"`
	Type string `json:"type"`
	JSON string `json:"json"`
}

// Struct is one exported struct type + its fields.
type Struct struct {
	Name    string  `json:"name"`
	Package string  `json:"package"`
	File    string  `json:"file"`
	Fields  []Field `json:"fields"`
}

// Dump is the top-level JSON envelope.
type Dump struct {
	Structs []Struct `json:"structs"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dump-contracts:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("dump-contracts", flag.ContinueOnError)
	out := fs.String("out", "", "write JSON to this file instead of stdout")
	filesArg := fs.String("files", "", "comma-separated list of Go files to scan (default: W0 contract files)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	files := defaultFiles
	if *filesArg != "" {
		files = strings.Split(*filesArg, ",")
	}

	dump, err := dumpFiles(files)
	if err != nil {
		return err
	}

	enc, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dump: %w", err)
	}

	if *out == "" {
		_, werr := stdout.Write(enc)
		if werr != nil {
			return werr
		}
		_, _ = stdout.Write([]byte{'\n'})
		return nil
	}
	if err := os.WriteFile(*out, append(enc, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	return nil
}

// defaultFiles is resolved relative to the current working directory.
// The Makefile / CI invokes the tool from the repo root.
var defaultFiles = []string{
	"internal/api/contracts.go",
	"internal/api/sse_events.go",
	"internal/connectors/connector.go",
}

// dumpFiles parses each file with go/parser and extracts every exported
// struct type declaration.
func dumpFiles(paths []string) (*Dump, error) {
	dump := &Dump{}
	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		pkg := file.Name.Name
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				dump.Structs = append(dump.Structs, Struct{
					Name:    ts.Name.Name,
					Package: pkg,
					File:    path,
					Fields:  extractFields(st),
				})
			}
		}
	}
	// Stable order so the dump is diff-friendly.
	sort.Slice(dump.Structs, func(i, j int) bool {
		if dump.Structs[i].Package != dump.Structs[j].Package {
			return dump.Structs[i].Package < dump.Structs[j].Package
		}
		return dump.Structs[i].Name < dump.Structs[j].Name
	})
	return dump, nil
}

// extractFields walks a *ast.StructType and collects exported fields.
func extractFields(st *ast.StructType) []Field {
	var out []Field
	if st.Fields == nil {
		return out
	}
	for _, f := range st.Fields.List {
		// Anonymous / embedded fields are skipped — they don't appear in
		// any of W0's contract DTOs.
		if len(f.Names) == 0 {
			continue
		}
		typeStr := exprString(f.Type)
		jsonTag := ""
		if f.Tag != nil {
			raw := strings.Trim(f.Tag.Value, "`")
			tag := reflect.StructTag(raw)
			if v := tag.Get("json"); v != "" {
				// Strip ",omitempty" etc. — only the wire name matters.
				if comma := strings.IndexByte(v, ','); comma >= 0 {
					v = v[:comma]
				}
				jsonTag = v
			}
		}
		for _, n := range f.Names {
			if !n.IsExported() {
				continue
			}
			out = append(out, Field{
				Name: n.Name,
				Type: typeStr,
				JSON: jsonTag,
			})
		}
	}
	return out
}

// exprString renders an AST type expression back to its source-level
// form (e.g. `[]ToolCall`, `map[string]PerTokenRates`).
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(x.X)
	case *ast.ArrayType:
		return "[]" + exprString(x.Elt)
	case *ast.MapType:
		return "map[" + exprString(x.Key) + "]" + exprString(x.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.StructType:
		return "struct{...}"
	case *ast.FuncType:
		return "func(...)"
	default:
		return fmt.Sprintf("%T", e)
	}
}
