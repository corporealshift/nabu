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

// modulesPkg is the aggregator import path: the registration list, not a
// module.
const modulesPkg = repoModule + "/daemon/modules"

// TestModulesImportOnlyTheContract enforces spec §14.4: packages under
// daemon/modules may import the standard library, third-party code, the
// module contract, and protocol — never daemon internals.
//
// The aggregator package itself (files directly in daemon/modules, not in a
// subdirectory) is exempt from the module rule, because its whole job is to
// import each module and list it. It is held to a narrower rule instead: it
// may import the contract and sibling modules, and nothing else from the repo.
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
		isAggregator := filepath.Clean(filepath.Dir(path)) == filepath.Clean(root)

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(p, repoModule) {
				continue
			}
			if isAggregator {
				if p == repoModule+"/daemon/module" || strings.HasPrefix(p, modulesPkg+"/") {
					continue
				}
				violations = append(violations, filepath.ToSlash(path)+
					" is the registration list and may import only the contract and modules, not "+p)
				continue
			}
			if !allowedRepoImports[p] {
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
