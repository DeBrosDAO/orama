package main

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// portPackages are the packages whose port constants make up Appendix A.
var portPackages = []string{
	"./pkg/constants",
	"./pkg/namespace",
	"./pkg/deployments",
	"./pkg/turn",
	"./pkg/sfu",
}

// portNameRe selects the constants that are ports, port ranges or port
// block sizes.
var portNameRe = regexp.MustCompile(`Port|RangeStart|RangeEnd`)

type portConst struct {
	name, file, comment string
	value               int64
}

// genPorts renders Appendix A from the type-checked port constants.
func genPorts(b *Book) ([]byte, error) {
	pkgs, err := loadTyped(b, portPackages)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	sb.WriteString(generatedHeader(titleOf(b, "appendices/a-port-map.md"), "the port constants in "+strings.Join(trimDots(portPackages), ", ")))
	sb.WriteString("Every integer constant whose name marks it as a port, a port range bound or a port block size, sorted by value within each package. Chapter 2 explains the layout.\n")
	for _, p := range pkgs {
		consts := portConsts(b, p)
		if len(consts) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "\n## %s\n\n| Value | Constant | Meaning |\n|---|---|---|\n", strings.TrimPrefix(p.PkgPath, "github.com/DeBrosOfficial/network/"))
		for _, c := range consts {
			fmt.Fprintf(&sb, "| %d | `%s:%s` | %s |\n", c.value, c.file, c.name, tableCell(c.comment))
		}
	}
	return []byte(sb.String()), nil
}

// loadTyped loads core packages with types and syntax.
func loadTyped(b *Book, patterns []string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedFiles,
		Dir:  filepath.Join(b.Root, "core"),
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load %v: %w", patterns, err)
	}
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			return nil, fmt.Errorf("failed to type-check %s: %v", p.PkgPath, p.Errors[0])
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	return pkgs, nil
}

func portConsts(b *Book, p *packages.Package) []portConst {
	comments := constComments(p)
	var out []portConst
	scope := p.Types.Scope()
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if !ok || !c.Exported() || !portNameRe.MatchString(name) || c.Val().Kind() != constant.Int {
			continue
		}
		v, exact := constant.Int64Val(c.Val())
		if !exact {
			continue
		}
		pos := p.Fset.Position(c.Pos())
		file, _ := filepath.Rel(b.Root, pos.Filename)
		comment := comments[name]
		if comment == strconv.FormatInt(v, 10) {
			comment = "" // a trailing "// 10100" restates the value
		}
		out = append(out, portConst{name: name, value: v, file: filepath.ToSlash(file), comment: comment})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].value != out[j].value {
			return out[i].value < out[j].value
		}
		return out[i].name < out[j].name
	})
	return out
}

// constComments maps each constant to its doc or trailing comment, falling
// back to the doc of its declaration group.
func constComments(p *packages.Package) map[string]string {
	out := map[string]string{}
	for _, f := range p.Syntax {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				text := commentText(vs.Doc, vs.Comment)
				if text == "" && len(gd.Specs) == 1 {
					text = commentText(gd.Doc, nil)
				}
				for _, n := range vs.Names {
					out[n.Name] = text
				}
			}
		}
	}
	return out
}

func commentText(groups ...*ast.CommentGroup) string {
	for _, g := range groups {
		if g != nil {
			if t := strings.TrimSpace(g.Text()); t != "" {
				return t
			}
		}
	}
	return ""
}

// tableCell flattens text into one Markdown table cell that is safe for the
// website's MDX renderer.
func tableCell(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	return strings.NewReplacer("|", "\\|", "{", "&#123;", "}", "&#125;", "<", "&lt;").Replace(text)
}

func trimDots(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = "core/" + strings.TrimPrefix(p, "./")
	}
	return out
}
