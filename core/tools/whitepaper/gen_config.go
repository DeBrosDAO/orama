package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/packages"
)

// configRoot is one YAML file format and the Go type that decodes it.
type configRoot struct {
	pkg, typeName, file, about string
}

var configRoots = []configRoot{
	{"./pkg/config", "Config", "node.yaml", "The node's configuration, written by `orama node install` and read by `orama-node`."},
	{"./pkg/gatewayspec", "GatewayYAMLConfig", "gateway.yaml (namespace gateway)", "A namespace gateway's configuration, rendered by the namespace spawner and read by `orama-gateway`."},
}

// maxConfigDepth stops recursion through self-referencing types.
const maxConfigDepth = 6

type configKey struct{ key, typ, comment string }

// genConfiguration renders Appendix F from the Go types that decode each
// configuration file.
func genConfiguration(b *Book) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString(generatedHeader(titleOf(b, "appendices/f-configuration.md"), "the YAML-tagged Go types that decode each file"))
	for _, root := range configRoots {
		pkgs, err := loadTyped(b, []string{root.pkg})
		if err != nil {
			return nil, err
		}
		p := pkgs[0]
		obj := p.Types.Scope().Lookup(root.typeName)
		if obj == nil {
			return nil, fmt.Errorf("type %s not found in %s", root.typeName, root.pkg)
		}
		comments := fieldComments(p)
		var keys []configKey
		walkConfig(obj.Type(), "", comments, 0, &keys)
		fmt.Fprintf(&sb, "\n## %s\n\n%s Decoded by `core/%s:%s`.\n\n| Key | Type | Meaning |\n|---|---|---|\n",
			root.file, root.about, strings.TrimPrefix(root.pkg, "./"), root.typeName)
		for _, k := range keys {
			fmt.Fprintf(&sb, "| `%s` | `%s` | %s |\n", k.key, tableCell(k.typ), tableCell(k.comment))
		}
	}
	return []byte(sb.String()), nil
}

// walkConfig appends one key per YAML-tagged field, recursing into structs.
func walkConfig(t types.Type, prefix string, comments map[token.Pos]string, depth int, out *[]configKey) {
	st, ok := structOf(t)
	if !ok || depth > maxConfigDepth {
		return
	}
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		name, inline := yamlName(st.Tag(i), f.Name())
		if name == "-" || !f.Exported() {
			continue
		}
		key := joinKey(prefix, name)
		if inline {
			walkConfig(f.Type(), prefix, comments, depth+1, out)
			continue
		}
		*out = append(*out, configKey{key: key, typ: shortType(f.Type()), comment: comments[f.Pos()]})
		walkConfig(f.Type(), key, comments, depth+1, out)
	}
}

// structOf unwraps pointers, slices and maps down to a struct.
func structOf(t types.Type) (*types.Struct, bool) {
	for {
		switch u := t.Underlying().(type) {
		case *types.Pointer:
			t = u.Elem()
		case *types.Slice:
			t = u.Elem()
		case *types.Map:
			t = u.Elem()
		case *types.Struct:
			return u, true
		default:
			return nil, false
		}
	}
}

func yamlName(tag, field string) (string, bool) {
	v, ok := reflect.StructTag(tag).Lookup("yaml")
	if !ok {
		return strings.ToLower(field), false
	}
	name, opts, _ := strings.Cut(v, ",")
	inline := strings.Contains(opts, "inline")
	if name == "" {
		name = strings.ToLower(field)
	}
	return name, inline
}

func joinKey(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func shortType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return p.Name() })
}

// fieldComments maps each struct field's position to its doc or trailing
// comment.
func fieldComments(p *packages.Package) map[token.Pos]string {
	out := map[token.Pos]string{}
	for _, f := range p.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			field, ok := n.(*ast.Field)
			if !ok {
				return true
			}
			text := commentText(field.Doc, field.Comment)
			for _, name := range field.Names {
				out[name.Pos()] = text
			}
			return true
		})
	}
	return out
}
