// Copyright 2018 The CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package internal exposes some cue internals to other packages.
//
// A better name for this package would be technicaldebt.
package internal // import "cuelang.org/go/internal"

// TODO: refactor packages as to make this package unnecessary.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/apd/v2"
	"golang.org/x/xerrors"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
)

// A Decimal is an arbitrary-precision binary-coded decimal number.
//
// Right now Decimal is aliased to apd.Decimal. This may change in the future.
type Decimal = apd.Decimal

// DebugStr prints a syntax node.
var DebugStr func(x interface{}) string

// ErrIncomplete can be used by builtins to signal the evaluation was
// incomplete.
var ErrIncomplete = errors.New("incomplete value")

// EvalExpr evaluates an expression within an existing struct value.
// Identifiers only resolve to values defined within the struct.
//
// Expressions may refer to builtin packages if they can be uniquely identified
//
// Both value and result are of type cue.Value, but are an interface to prevent
// cyclic dependencies.
//
// TODO: extract interface
var EvalExpr func(value, expr interface{}) (result interface{})

// FromGoValue converts an arbitrary Go value to the corresponding CUE value.
// instance must be of type *cue.Instance.
// The returned value is a cue.Value, which the caller must cast to.
var FromGoValue func(instance, x interface{}, allowDefault bool) interface{}

// FromGoType converts an arbitrary Go type to the corresponding CUE value.
// instance must be of type *cue.Instance.
// The returned value is a cue.Value, which the caller must cast to.
var FromGoType func(instance, x interface{}) interface{}

// UnifyBuiltin returns the given Value unified with the given builtin template.
var UnifyBuiltin func(v interface{}, kind string) interface{}

// GetRuntime reports the runtime for an Instance or Value.
var GetRuntimeOld func(instance interface{}) interface{}

// GetRuntime reports the runtime for an Instance or Value.
var GetRuntimeNew func(instance interface{}) interface{}

// CoreValue returns an *runtime.Index and *adt.Vertex for a cue.Value.
// It returns nil if value is not a cue.Value.
var CoreValue func(value interface{}) (runtime, vertex interface{})

// MakeInstance makes a new instance from a value.
var MakeInstance func(value interface{}) (instance interface{})

// CheckAndForkRuntime checks that value is created using runtime, panicking
// if it does not, and returns a forked runtime that will discard additional
// keys.
var CheckAndForkRuntimeOld func(runtime, value interface{}) interface{}

// CheckAndForkRuntime checks that value is created using runtime, panicking
// if it does not, and returns a forked runtime that will discard additional
// keys.
var CheckAndForkRuntimeNew func(runtime, value interface{}) interface{}

// BaseContext is used as CUEs default context for arbitrary-precision decimals
var BaseContext = apd.BaseContext.WithPrecision(24)

// ListEllipsis reports the list type and remaining elements of a list. If we
// ever relax the usage of ellipsis, this function will likely change. Using
// this function will ensure keeping correct behavior or causing a compiler
// failure.
func ListEllipsis(n *ast.ListLit) (elts []ast.Expr, e *ast.Ellipsis) {
	elts = n.Elts
	if n := len(elts); n > 0 {
		var ok bool
		if e, ok = elts[n-1].(*ast.Ellipsis); ok {
			elts = elts[:n-1]
		}
	}
	return elts, e
}

func PackageInfo(f *ast.File) (p *ast.Package, name string, tok token.Pos) {
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.CommentGroup:
		case *ast.Attribute:
		case *ast.Package:
			if x.Name == nil {
				break
			}
			return x, x.Name.Name, x.Name.Pos()
		}
	}
	return nil, "", f.Pos()
}

func SetPackage(f *ast.File, name string, overwrite bool) {
	p, str, _ := PackageInfo(f)
	if p != nil {
		if !overwrite || str == name {
			return
		}
		ident := ast.NewIdent(name)
		astutil.CopyMeta(ident, p.Name)
		return
	}

	decls := make([]ast.Decl, len(f.Decls)+1)
	k := 0
	for _, d := range f.Decls {
		if _, ok := d.(*ast.CommentGroup); ok {
			decls[k] = d
			k++
			continue
		}
		break
	}
	decls[k] = &ast.Package{Name: ast.NewIdent(name)}
	copy(decls[k+1:], f.Decls[k:])
	f.Decls = decls
}

// NewComment creates a new CommentGroup from the given text.
// Each line is prefixed with "//" and the last newline is removed.
// Useful for ASTs generated by code other than the CUE parser.
func NewComment(isDoc bool, s string) *ast.CommentGroup {
	if s == "" {
		return nil
	}
	cg := &ast.CommentGroup{Doc: isDoc}
	if !isDoc {
		cg.Line = true
		cg.Position = 10
	}
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		scanner := bufio.NewScanner(strings.NewReader(scanner.Text()))
		scanner.Split(bufio.ScanWords)
		const maxRunesPerLine = 66
		count := 2
		buf := strings.Builder{}
		buf.WriteString("//")
		for scanner.Scan() {
			s := scanner.Text()
			n := len([]rune(s)) + 1
			if count+n > maxRunesPerLine && count > 3 {
				cg.List = append(cg.List, &ast.Comment{Text: buf.String()})
				count = 3
				buf.Reset()
				buf.WriteString("//")
			}
			buf.WriteString(" ")
			buf.WriteString(s)
			count += n
		}
		cg.List = append(cg.List, &ast.Comment{Text: buf.String()})
	}
	if last := len(cg.List) - 1; cg.List[last].Text == "//" {
		cg.List = cg.List[:last]
	}
	return cg
}

func FileComment(f *ast.File) *ast.CommentGroup {
	pkg, _, _ := PackageInfo(f)
	var cgs []*ast.CommentGroup
	if pkg != nil {
		cgs = pkg.Comments()
	} else if cgs = f.Comments(); len(cgs) > 0 {
		// Use file comment.
	} else {
		// Use first comment before any declaration.
		for _, d := range f.Decls {
			if cg, ok := d.(*ast.CommentGroup); ok {
				return cg
			}
			if cgs = ast.Comments(d); cgs != nil {
				break
			}
			// TODO: what to do here?
			if _, ok := d.(*ast.Attribute); !ok {
				break
			}
		}
	}
	var cg *ast.CommentGroup
	for _, c := range cgs {
		if c.Position == 0 {
			cg = c
		}
	}
	return cg
}

func NewAttr(name, str string) *ast.Attribute {
	buf := &strings.Builder{}
	buf.WriteByte('@')
	buf.WriteString(name)
	buf.WriteByte('(')
	fmt.Fprintf(buf, str)
	buf.WriteByte(')')

	return &ast.Attribute{Text: buf.String()}
}

// ToExpr converts a node to an expression. If it is a file, it will return
// it as a struct. If is an expression, it will return it as is. Otherwise
// it panics.
func ToExpr(n ast.Node) ast.Expr {
	switch x := n.(type) {
	case nil:
		return nil

	case ast.Expr:
		return x

	case *ast.File:
		start := 0
	outer:
		for i, d := range x.Decls {
			switch d.(type) {
			case *ast.Package, *ast.ImportDecl:
				start = i + 1
			case *ast.CommentGroup, *ast.Attribute:
			default:
				break outer
			}
		}
		decls := x.Decls[start:]
		if len(decls) == 1 {
			if e, ok := decls[0].(*ast.EmbedDecl); ok {
				return e.Expr
			}
		}
		return &ast.StructLit{Elts: decls}

	default:
		panic(fmt.Sprintf("Unsupported node type %T", x))
	}
}

// ToFile converts an expression to a file.
//
// Adjusts the spacing of x when needed.
func ToFile(n ast.Node) *ast.File {
	switch x := n.(type) {
	case nil:
		return nil
	case *ast.StructLit:
		return &ast.File{Decls: x.Elts}
	case ast.Expr:
		ast.SetRelPos(x, token.NoSpace)
		return &ast.File{Decls: []ast.Decl{&ast.EmbedDecl{Expr: x}}}
	case *ast.File:
		return x
	default:
		panic(fmt.Sprintf("Unsupported node type %T", x))
	}
}

// ToStruct gets the non-preamble declarations of a file and puts them in a
// struct.
func ToStruct(f *ast.File) *ast.StructLit {
	start := 0
	for i, d := range f.Decls {
		switch d.(type) {
		case *ast.Package, *ast.ImportDecl:
			start = i + 1
		case *ast.Attribute, *ast.CommentGroup:
		default:
			break
		}
	}
	s := ast.NewStruct()
	s.Elts = f.Decls[start:]
	return s
}

func IsBulkField(d ast.Decl) bool {
	if f, ok := d.(*ast.Field); ok {
		if _, ok := f.Label.(*ast.ListLit); ok {
			return true
		}
	}
	return false
}

func IsDef(s string) bool {
	return strings.HasPrefix(s, "#") || strings.HasPrefix(s, "_#")
}

func IsHidden(s string) bool {
	return strings.HasPrefix(s, "_")
}

func IsDefOrHidden(s string) bool {
	return strings.HasPrefix(s, "#") || strings.HasPrefix(s, "_")
}

func IsDefinition(label ast.Label) bool {
	switch x := label.(type) {
	case *ast.Alias:
		if ident, ok := x.Expr.(*ast.Ident); ok {
			return IsDef(ident.Name)
		}
	case *ast.Ident:
		return IsDef(x.Name)
	}
	return false
}

func IsRegularField(f *ast.Field) bool {
	if f.Token == token.ISA {
		return false
	}
	var ident *ast.Ident
	switch x := f.Label.(type) {
	case *ast.Alias:
		ident, _ = x.Expr.(*ast.Ident)
	case *ast.Ident:
		ident = x
	}
	if ident == nil {
		return true
	}
	if strings.HasPrefix(ident.Name, "#") || strings.HasPrefix(ident.Name, "_") {
		return false
	}
	return true
}

func EmbedStruct(s *ast.StructLit) *ast.EmbedDecl {
	e := &ast.EmbedDecl{Expr: s}
	if len(s.Elts) == 1 {
		d := s.Elts[0]
		astutil.CopyPosition(e, d)
		ast.SetRelPos(d, token.NoSpace)
		astutil.CopyComments(e, d)
		ast.SetComments(d, nil)
		if f, ok := d.(*ast.Field); ok {
			ast.SetRelPos(f.Label, token.NoSpace)
		}
	}
	s.Lbrace = token.Newline.Pos()
	s.Rbrace = token.NoSpace.Pos()
	return e
}

// IsEllipsis reports whether the declaration can be represented as an ellipsis.
func IsEllipsis(x ast.Decl) bool {
	// ...
	if _, ok := x.(*ast.Ellipsis); ok {
		return true
	}

	// [string]: _ or [_]: _
	f, ok := x.(*ast.Field)
	if !ok {
		return false
	}
	v, ok := f.Value.(*ast.Ident)
	if !ok || v.Name != "_" {
		return false
	}
	l, ok := f.Label.(*ast.ListLit)
	if !ok || len(l.Elts) != 1 {
		return false
	}
	i, ok := l.Elts[0].(*ast.Ident)
	if !ok {
		return false
	}
	return i.Name == "string" || i.Name == "_"
}

// GenPath reports the directory in which to store generated files.
func GenPath(root string) string {
	info, err := os.Stat(filepath.Join(root, "cue.mod"))
	if os.IsNotExist(err) || !info.IsDir() {
		// Try legacy pkgDir mode
		pkgDir := filepath.Join(root, "pkg")
		if err == nil && !info.IsDir() {
			return pkgDir
		}
		if info, err := os.Stat(pkgDir); err == nil && info.IsDir() {
			return pkgDir
		}
	}
	return filepath.Join(root, "cue.mod", "gen")
}

var ErrInexact = errors.New("inexact subsumption")

func DecorateError(info error, err errors.Error) errors.Error {
	return &decorated{cueError: err, info: info}
}

type cueError = errors.Error

type decorated struct {
	cueError

	info error
}

func (e *decorated) Is(err error) bool {
	return xerrors.Is(e.info, err) || xerrors.Is(e.cueError, err)
}

// MaxDepth indicates the maximum evaluation depth. This is there to break
// cycles in the absence of cycle detection.
//
// It is registered in a central place to make it easy to find all spots where
// cycles are broken in this brute-force manner.
//
// TODO(eval): have cycle detection.
const MaxDepth = 20
