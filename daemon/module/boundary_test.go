package module

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const repoModule = "github.com/corporealshift/nabu"

// allowedRepoImports are the only in-repo packages a module may import.
var allowedRepoImports = map[string]bool{
	repoModule + "/daemon/module": true,
	repoModule + "/protocol":      true,
}

// TestModulesImportOnlyTheContract enforces spec §14.4: packages under
// daemon/modules may import the standard library, third-party code, the
// module contract, and protocol — never daemon internals.
func TestModulesImportOnlyTheContract(t *testing.T) {
	root := filepath.Join("..", "modules")
	if _, err := os.Stat(root); err != nil {
		t.Skip("no daemon/modules directory")
	}
	fset := token.NewFileSet()
	var violations []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if strings.HasPrefix(p, repoModule) && !allowedRepoImports[p] {
				violations = append(violations, filepath.ToSlash(path)+" imports "+p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}
