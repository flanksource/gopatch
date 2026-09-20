package typed

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/decorator/resolver/simple"
	"golang.org/x/tools/go/packages"
)

// MoveRequest describes a file or declaration relocation.
type MoveRequest struct {
	Symbol      *Symbol
	SourceFile  string
	Destination string
	StructOnly  bool
}

// MoveRef identifies a package-level declaration whose import path changes.
type MoveRef struct {
	Kind       Kind
	ImportPath string
	Name       string
}

// MoveResult is a fully rendered relocation and its user-facing summary data.
type MoveResult struct {
	Edits                          []FileEdit
	Refs                           []MoveRef
	SourcePackage, TargetPackage   string
	TargetPackageName              string
	SourceFile, DestinationFile    string
	Declarations                   []string
	References                     int
	SourcePackageEntirelyRelocated bool
}

type moveDestination struct {
	path, pkgPath, pkgName string
	file                   *decoratedFile
}

type moveSpan struct {
	filename   string
	start, end int
}

type movePiece struct {
	pkg        *decorator.Package
	source     *decoratedFile
	astNode    ast.Node
	decl       dst.Decl
	remove     func()
	label      string
	identifier token.Pos
}

// ValidateMove type-checks a rendered change set through a go/packages overlay.
func ValidateMove(root string, edits []FileEdit) error {
	overlay := make(map[string][]byte, len(edits))
	for _, edit := range edits {
		if edit.Delete {
			file, err := parser.ParseFile(token.NewFileSet(), edit.Path, edit.Original, parser.PackageClauseOnly)
			if err != nil {
				return fmt.Errorf("parse deleted file %v: %w", edit.Path, err)
			}
			overlay[edit.Path] = []byte("package " + file.Name.Name + "\n")
			continue
		}
		overlay[edit.Path] = edit.Modified
	}
	loaded, err := packages.Load(&packages.Config{
		Dir: root, Mode: _loadMode, Tests: true, Overlay: overlay,
		Env: append(os.Environ(), "GO111MODULE=on"),
	}, "./...")
	if err != nil {
		return fmt.Errorf("validate move in %v: %w", root, err)
	}
	if err := packageErrors(loaded); err != nil {
		return fmt.Errorf("the move would not compile in %v: %w", root, err)
	}
	return nil
}

func packageErrors(pkgs []*packages.Package) error {
	seen := make(map[string]bool)
	var messages []string
	packages.Visit(pkgs, nil, func(pkg *packages.Package) {
		for _, pkgErr := range pkg.Errors {
			message := pkgErr.Error()
			if !seen[message] {
				seen[message] = true
				messages = append(messages, message)
			}
		}
	})
	if len(messages) == 0 {
		return nil
	}
	sort.Strings(messages)
	return errors.New(strings.Join(messages, "; "))
}

// Move relocates one file or one package-level declaration.
func (p *Program) Move(req MoveRequest) (MoveResult, error) {
	if (req.Symbol == nil) == (req.SourceFile == "") {
		return MoveResult{}, fmt.Errorf("move needs exactly one symbol or source file")
	}
	if filepath.Ext(req.Destination) != ".go" {
		return MoveResult{}, fmt.Errorf("destination %v is not a Go file", req.Destination)
	}

	dest, err := p.moveDestination(req.Destination)
	if err != nil {
		return MoveResult{}, err
	}
	if req.SourceFile != "" {
		return p.moveFile(req.SourceFile, dest)
	}
	return p.moveSymbol(req.Symbol, dest, req.StructOnly)
}

func (p *Program) moveDestination(path string) (moveDestination, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return moveDestination{}, err
	}
	abs = realFuturePath(abs)
	rel, err := filepath.Rel(p.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return moveDestination{}, fmt.Errorf("destination %v is outside module %v", path, p.root)
	}

	if df := p.files[abs]; df != nil {
		return moveDestination{path: abs, pkgPath: df.pkg.PkgPath, pkgName: df.pkg.Name, file: df}, nil
	}
	if _, err := os.Stat(abs); err == nil {
		return moveDestination{}, fmt.Errorf("destination %v exists but is not a loaded Go file", path)
	} else if !os.IsNotExist(err) {
		return moveDestination{}, err
	}

	dir := filepath.Dir(abs)
	var choices []moveDestination
	seen := make(map[string]bool)
	for filePath, df := range p.files {
		if filepath.Dir(filePath) != dir || seen[df.pkg.PkgPath] || strings.HasSuffix(filePath, "_test.go") {
			continue
		}
		seen[df.pkg.PkgPath] = true
		choices = append(choices, moveDestination{path: abs, pkgPath: df.pkg.PkgPath, pkgName: df.pkg.Name})
	}
	if len(choices) == 1 {
		return choices[0], nil
	}
	if len(choices) > 1 {
		return moveDestination{}, fmt.Errorf("destination directory %v contains more than one package", dir)
	}

	name := filepath.Base(dir)
	if !token.IsIdentifier(name) {
		return moveDestination{}, fmt.Errorf("destination directory %v does not imply a valid package name", dir)
	}
	if p.modulePath == "" {
		return moveDestination{}, fmt.Errorf("could not determine the module import path for %v", p.root)
	}
	pkgPath := p.modulePath
	if relDir, err := filepath.Rel(p.root, dir); err == nil && relDir != "." {
		pkgPath += "/" + filepath.ToSlash(relDir)
	}
	return moveDestination{path: abs, pkgPath: pkgPath, pkgName: name}, nil
}

func realFuturePath(path string) string {
	current := path
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for left, right := 0, len(suffix)-1; left < right; left, right = left+1, right-1 {
				suffix[left], suffix[right] = suffix[right], suffix[left]
			}
			parts := append([]string{resolved}, suffix...)
			return filepath.Join(parts...)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func (p *Program) moveFile(source string, dest moveDestination) (MoveResult, error) {
	abs, err := filepath.Abs(source)
	if err != nil {
		return MoveResult{}, err
	}
	abs = realPath(abs)
	df := p.files[abs]
	if df == nil {
		return MoveResult{}, fmt.Errorf("%v is not a loaded Go file in this module", source)
	}
	if dest.file != nil {
		return MoveResult{}, fmt.Errorf("destination %v already exists", dest.path)
	}
	if abs == dest.path {
		return MoveResult{}, fmt.Errorf("source and destination are the same file")
	}
	if err := compatibleMoveFiles([]string{abs}, dest.path, dest.file != nil, true); err != nil {
		return MoveResult{}, err
	}

	astFile, _ := df.pkg.Decorator.Ast.Nodes[df.file].(*ast.File)
	if astFile == nil {
		return MoveResult{}, fmt.Errorf("could not locate syntax for %v", source)
	}
	entirePackage := p.packageFileCount(df.pkg.PkgPath) == 1
	if df.pkg.PkgPath != dest.pkgPath && importsC(astFile) {
		return MoveResult{}, fmt.Errorf("cannot move cgo file %v to another package safely", source)
	}
	if df.pkg.PkgPath != dest.pkgPath && hasInitFunction(astFile) && p.packageFileCount(df.pkg.PkgPath) > 1 {
		return MoveResult{}, fmt.Errorf("cannot move %v to another package because its init function would change package initialization ownership", source)
	}
	refs := p.topLevelRefs(df.pkg, astFile)
	spans := declarationSpans(df.pkg, astFile.Decls)
	if df.pkg.PkgPath != dest.pkgPath {
		if err := p.checkDestinationNames(dest, refs); err != nil {
			return MoveResult{}, err
		}
	}
	if err := p.qualifyMovedDependencies(df.pkg.PkgPath, dest.pkgPath, spans, refs); err != nil {
		return MoveResult{}, err
	}
	references := 0
	if df.pkg.PkgPath != dest.pkgPath {
		references = p.redirectRefs(refs, dest.pkgPath, spans)
		if entirePackage {
			p.redirectMoveImports(map[string]bool{df.pkg.PkgPath: true}, dest.pkgPath)
		}
	}

	oldPackage := df.pkg.PkgPath
	oldName := df.file.Name.Name
	df.file.Name.Name = dest.pkgName
	p.names[dest.pkgPath] = dest.pkgName
	modified, err := p.renderFile(dest.pkgPath, df.file, nil)
	if err != nil {
		df.file.Name.Name = oldName
		return MoveResult{}, err
	}
	df.file.Name.Name = oldName

	original, mode, err := readFileState(abs)
	if err != nil {
		return MoveResult{}, err
	}
	edits, err := p.moveSupportingEdits(map[string]bool{abs: true})
	if err != nil {
		return MoveResult{}, err
	}
	edits = append(edits,
		FileEdit{Path: abs, Original: original, Delete: true, Mode: mode},
		FileEdit{Path: dest.path, Modified: modified, Create: true, Mode: mode},
	)

	result := MoveResult{
		Edits: edits, Refs: refs, SourcePackage: oldPackage, TargetPackage: dest.pkgPath,
		TargetPackageName: dest.pkgName, SourceFile: abs, DestinationFile: dest.path, References: references,
		SourcePackageEntirelyRelocated: entirePackage,
	}
	for _, ref := range refs {
		result.Declarations = append(result.Declarations, ref.Kind.String()+" "+ref.Name)
	}
	result.Declarations = uniqueSorted(result.Declarations)
	return result, nil
}

func (p *Program) moveSymbol(sym *Symbol, dest moveDestination, structOnly bool) (MoveResult, error) {
	pieces, refs, err := p.symbolPieces(sym)
	if err != nil {
		return MoveResult{}, err
	}
	if structOnly {
		if named, ok := firstObject(sym).(*types.TypeName); !ok {
			return MoveResult{}, fmt.Errorf("%s is not a type", sym.Desc)
		} else if _, ok := named.Type().Underlying().(*types.Struct); !ok {
			return MoveResult{}, fmt.Errorf("%s is not a struct", sym.Desc)
		}
	}
	if len(pieces) == 0 {
		return MoveResult{}, fmt.Errorf("could not locate %s", sym.Desc)
	}
	sourceFiles := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		sourceFiles = append(sourceFiles, piece.source.path)
	}
	if err := compatibleMoveFiles(uniqueSorted(sourceFiles), dest.path, dest.file != nil, false); err != nil {
		return MoveResult{}, err
	}
	sourcePkg := pieces[0].pkg.PkgPath
	if sym.Kind == KindMethod && sourcePkg != dest.pkgPath {
		return MoveResult{}, fmt.Errorf("a method must remain in the package that declares its receiver; move the receiver type instead")
	}
	if sourcePkg != dest.pkgPath {
		for _, piece := range pieces {
			astFile, _ := piece.pkg.Decorator.Ast.Nodes[piece.source.file].(*ast.File)
			if importsC(astFile) {
				return MoveResult{}, fmt.Errorf("cannot move declarations from cgo file %v to another package safely", piece.source.path)
			}
		}
	}
	for _, piece := range pieces {
		if piece.source.path == dest.path {
			return MoveResult{}, fmt.Errorf("%s is already in %v", sym.Desc, dest.path)
		}
	}
	if sourcePkg != dest.pkgPath {
		if err := p.checkDestinationNames(dest, refs); err != nil {
			return MoveResult{}, err
		}
	}

	spans := pieceSpans(pieces)
	if err := p.qualifyMovedDependencies(sourcePkg, dest.pkgPath, spans, refs); err != nil {
		return MoveResult{}, err
	}
	references := 0
	if sourcePkg != dest.pkgPath {
		references = p.redirectRefs(refs, dest.pkgPath, spans)
	}

	for _, piece := range pieces {
		piece.remove()
		p.mark(piece.source)
	}

	p.names[dest.pkgPath] = dest.pkgName
	var destination *dst.File
	if dest.file != nil {
		destination = dest.file.file
		p.mark(dest.file)
	} else {
		destination = &dst.File{Name: dst.NewIdent(dest.pkgName)}
	}
	for _, piece := range pieces {
		destination.Decls = append(destination.Decls, piece.decl)
	}

	deleted := make(map[string]bool)
	for _, piece := range pieces {
		if hasDeclarations(piece.source.file) {
			continue
		}
		deleted[piece.source.path] = true
	}
	entirePackage := p.packageFilesDeleted(sourcePkg, deleted)
	if sourcePkg != dest.pkgPath && entirePackage {
		p.redirectMoveImports(map[string]bool{sourcePkg: true}, dest.pkgPath)
	}
	edits, err := p.moveSupportingEdits(deleted)
	if err != nil {
		return MoveResult{}, err
	}
	for path := range deleted {
		original, mode, err := readFileState(path)
		if err != nil {
			return MoveResult{}, err
		}
		edits = append(edits, FileEdit{Path: path, Original: original, Delete: true, Mode: mode})
	}
	if dest.file == nil {
		modified, err := p.renderFile(dest.pkgPath, destination, nil)
		if err != nil {
			return MoveResult{}, err
		}
		info, err := os.Stat(pieces[0].source.path)
		if err != nil {
			return MoveResult{}, err
		}
		edits = append(edits, FileEdit{Path: dest.path, Modified: modified, Create: true, Mode: info.Mode().Perm()})
	}

	result := MoveResult{
		Edits: edits, Refs: refs, SourcePackage: sourcePkg, TargetPackage: dest.pkgPath,
		TargetPackageName: dest.pkgName, SourceFile: pieces[0].source.path, DestinationFile: dest.path, References: references,
		SourcePackageEntirelyRelocated: entirePackage,
	}
	for _, piece := range pieces {
		result.Declarations = append(result.Declarations, piece.label)
	}
	result.Declarations = uniqueSorted(result.Declarations)
	return result, nil
}

func (p *Program) symbolPieces(sym *Symbol) ([]movePiece, []MoveRef, error) {
	var pieces []movePiece
	seen := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.PkgPath != sym.Pkg || pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Defs {
			if !sym.objs[obj] {
				continue
			}
			piece, err := p.pieceForDefinition(pkg, id, sym.Kind.String()+" "+id.Name)
			if err != nil {
				return nil, nil, err
			}
			if piece == nil {
				continue
			}
			key := fmt.Sprintf("%s:%d", piece.source.path, piece.identifier)
			if !seen[key] {
				seen[key] = true
				pieces = append(pieces, *piece)
			}
		}
	}
	if sym.Kind == KindType {
		methods, err := p.receiverMethodPieces(sym.Pkg, sym.Name(), seen)
		if err != nil {
			return nil, nil, err
		}
		pieces = append(pieces, methods...)
	}
	refs := make([]MoveRef, 0, len(pieces))
	for _, piece := range pieces {
		if ref, ok := p.definitionRef(piece.pkg, piece.identifier); ok {
			refs = append(refs, ref)
		}
	}
	return pieces, uniqueRefs(refs), nil
}

func (p *Program) receiverMethodPieces(pkgPath, recv string, seen map[string]bool) ([]movePiece, error) {
	var pieces []movePiece
	for _, pkg := range p.pkgs {
		if pkg.PkgPath != pkgPath || pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Package.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil {
					continue
				}
				obj, _ := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
				owner := receiverNamed(obj)
				if owner == nil || owner.Obj().Name() != recv || owner.Obj().Pkg().Path() != pkgPath {
					continue
				}
				piece, err := p.pieceForDefinition(pkg, fn.Name, "method "+recv+"."+fn.Name.Name)
				if err != nil {
					return nil, err
				}
				if piece == nil {
					continue
				}
				key := fmt.Sprintf("%s:%d", piece.source.path, piece.identifier)
				if !seen[key] {
					seen[key] = true
					pieces = append(pieces, *piece)
				}
			}
		}
	}
	return pieces, nil
}

func (p *Program) pieceForDefinition(pkg *decorator.Package, id *ast.Ident, label string) (*movePiece, error) {
	astDecl := enclosingDecl(pkg, id)
	if astDecl == nil {
		return nil, nil
	}
	df := p.ownedFile(pkg, id)
	if df == nil {
		return nil, nil
	}
	dstDecl, _ := pkg.Decorator.Dst.Nodes[astDecl].(dst.Decl)
	if dstDecl == nil {
		return nil, fmt.Errorf("could not decorate declaration at %v", p.pos(pkg, id.Pos()))
	}
	piece := &movePiece{pkg: pkg, source: df, astNode: astDecl, decl: dstDecl, label: label, identifier: id.Pos()}

	gen, isGen := dstDecl.(*dst.GenDecl)
	astGen, astIsGen := astDecl.(*ast.GenDecl)
	if !isGen || !astIsGen {
		piece.remove = func() { removeDecl(df.file, dstDecl) }
		return piece, nil
	}

	var astSpec ast.Spec
	for _, spec := range astGen.Specs {
		if spec.Pos() <= id.Pos() && id.Pos() < spec.End() {
			astSpec = spec
			break
		}
	}
	if astSpec == nil {
		return nil, fmt.Errorf("could not locate declaration spec at %v", p.pos(pkg, id.Pos()))
	}
	dstSpec, _ := pkg.Decorator.Dst.Nodes[astSpec].(dst.Spec)
	if dstSpec == nil {
		return nil, fmt.Errorf("could not decorate declaration spec at %v", p.pos(pkg, id.Pos()))
	}
	if values, ok := dstSpec.(*dst.ValueSpec); ok {
		if len(values.Names) != 1 {
			return nil, fmt.Errorf("%s shares one declaration with other names; move that declaration manually", label)
		}
		if gen.Tok == token.CONST && (len(values.Values) == 0 || containsIota(values)) {
			return nil, fmt.Errorf("%s depends on an implicit value or iota and cannot be moved safely", label)
		}
	}
	if len(gen.Specs) == 1 {
		piece.remove = func() { removeDecl(df.file, dstDecl) }
		return piece, nil
	}
	piece.decl = &dst.GenDecl{Tok: gen.Tok, Specs: []dst.Spec{dstSpec}}
	piece.remove = func() { removeSpec(gen, dstSpec) }
	return piece, nil
}

func (p *Program) topLevelRefs(pkg *decorator.Package, node ast.Node) []MoveRef {
	var refs []MoveRef
	for id, obj := range pkg.TypesInfo.Defs {
		if id.Pos() < node.Pos() || id.Pos() >= node.End() {
			continue
		}
		if ref, ok := objectRef(obj); ok {
			refs = append(refs, ref)
		}
	}
	return uniqueRefs(refs)
}

func (p *Program) definitionRef(pkg *decorator.Package, pos token.Pos) (MoveRef, bool) {
	for id, obj := range pkg.TypesInfo.Defs {
		if id.Pos() == pos {
			return objectRef(obj)
		}
	}
	return MoveRef{}, false
}

func objectRef(obj types.Object) (MoveRef, bool) {
	if obj == nil || obj.Pkg() == nil || obj.Parent() != obj.Pkg().Scope() {
		return MoveRef{}, false
	}
	ref := MoveRef{ImportPath: obj.Pkg().Path(), Name: obj.Name()}
	switch obj.(type) {
	case *types.TypeName:
		ref.Kind = KindType
	case *types.Func:
		ref.Kind = KindFunc
	case *types.Var:
		ref.Kind = KindVar
	case *types.Const:
		ref.Kind = KindConst
	default:
		return MoveRef{}, false
	}
	return ref, true
}

func (p *Program) qualifyMovedDependencies(sourcePkg, destPkg string, spans []moveSpan, moved []MoveRef) error {
	var blocked []string
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Uses {
			if !positionWithin(pkg, id.Pos(), spans) {
				continue
			}
			ref, ok := objectRef(obj)
			if !ok {
				continue
			}
			switch {
			case containsRef(moved, ref):
				p.setIdentPath(pkg, id, "")
			case ref.ImportPath == sourcePkg:
				if !obj.Exported() {
					blocked = append(blocked, p.pos(pkg, id.Pos())+" ("+obj.Name()+")")
					continue
				}
				p.setIdentPath(pkg, id, sourcePkg)
			case ref.ImportPath == destPkg:
				p.setIdentPath(pkg, id, "")
			}
		}
	}
	if len(blocked) > 0 {
		sort.Strings(blocked)
		return fmt.Errorf("moved code depends on unexported declarations that remain in %v: %s", sourcePkg, summarize(blocked))
	}
	return nil
}

// RedirectMoves rewrites references to declarations relocated by another module.
func (p *Program) RedirectMoves(refs []MoveRef, targetPackage string, rewriteImports bool) (int, error) {
	count := p.redirectRefs(refs, targetPackage, nil)
	if rewriteImports {
		old := make(map[string]bool)
		for _, ref := range refs {
			old[ref.ImportPath] = true
		}
		p.redirectMoveImports(old, targetPackage)
	}
	return count, nil
}

func (p *Program) redirectMoveImports(old map[string]bool, targetPackage string) {
	for _, df := range p.files {
		for _, spec := range df.file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !old[path] {
				continue
			}
			spec.Path.Value = strconv.Quote(targetPackage)
			p.mark(df)
		}
	}
}

// RegisterPackage provides the package name used to render a newly created import path.
func (p *Program) RegisterPackage(importPath, name string) {
	p.names[importPath] = name
}

func (p *Program) redirectRefs(refs []MoveRef, targetPackage string, skip []moveSpan) int {
	changed := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Uses {
			ref, ok := objectRef(obj)
			if !ok || !containsRef(refs, ref) || positionWithin(pkg, id.Pos(), skip) {
				continue
			}
			path := targetPackage
			if pkg.PkgPath == targetPackage {
				path = ""
			}
			if pos, ok := p.setIdentPath(pkg, id, path); ok {
				changed[pos] = true
			}
		}
	}
	return len(changed)
}

func (p *Program) setIdentPath(pkg *decorator.Package, id *ast.Ident, path string) (string, bool) {
	node, ok := pkg.Decorator.Dst.Nodes[id]
	if !ok {
		return "", false
	}
	ident, ok := node.(*dst.Ident)
	if !ok {
		return "", false
	}
	df := p.ownedFile(pkg, id)
	if df == nil {
		return "", false
	}
	if ident.Path == path {
		return "", false
	}
	ident.Path = path
	p.mark(df)
	return pkg.Fset.Position(id.Pos()).String(), true
}

func (p *Program) checkDestinationNames(dest moveDestination, refs []MoveRef) error {
	for _, pkg := range p.pkgs {
		if pkg.PkgPath != dest.pkgPath || pkg.Types == nil {
			continue
		}
		for _, ref := range refs {
			if obj := pkg.Types.Scope().Lookup(ref.Name); obj != nil {
				return fmt.Errorf("destination package %v already declares %q at %v", dest.pkgPath, ref.Name, p.pos(pkg, obj.Pos()))
			}
		}
		break
	}
	return nil
}

func (p *Program) moveSupportingEdits(exclude map[string]bool) ([]FileEdit, error) {
	edits, err := p.Edits()
	if err != nil {
		return nil, err
	}
	out := edits[:0]
	for _, edit := range edits {
		if !exclude[edit.Path] {
			out = append(out, edit)
		}
	}
	return out, nil
}

func (p *Program) renderFile(pkgPath string, file *dst.File, aliases map[string]string) ([]byte, error) {
	r := decorator.NewRestorerWithImports(pkgPath, simple.New(p.names))
	fr := r.FileRestorer()
	for path, name := range aliases {
		fr.Alias[path] = name
	}
	restored, err := fr.RestoreFile(file)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, r.Fset, restored); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func readFileState(path string) ([]byte, os.FileMode, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}
	return contents, info.Mode().Perm(), nil
}

func declarationSpans(pkg *decorator.Package, decls []ast.Decl) []moveSpan {
	spans := make([]moveSpan, 0, len(decls))
	for _, decl := range decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		start := pkg.Fset.Position(decl.Pos())
		end := pkg.Fset.Position(decl.End())
		spans = append(spans, moveSpan{filename: realPath(start.Filename), start: start.Offset, end: end.Offset})
	}
	return spans
}

func pieceSpans(pieces []movePiece) []moveSpan {
	spans := make([]moveSpan, 0, len(pieces))
	for _, piece := range pieces {
		start := piece.pkg.Fset.Position(piece.astNode.Pos())
		end := piece.pkg.Fset.Position(piece.astNode.End())
		spans = append(spans, moveSpan{filename: realPath(start.Filename), start: start.Offset, end: end.Offset})
	}
	return spans
}

func positionWithin(pkg *decorator.Package, pos token.Pos, spans []moveSpan) bool {
	position := pkg.Fset.Position(pos)
	filename := realPath(position.Filename)
	for _, span := range spans {
		if span.filename == filename && position.Offset >= span.start && position.Offset < span.end {
			return true
		}
	}
	return false
}

func firstObject(sym *Symbol) types.Object {
	for obj := range sym.objs {
		return obj
	}
	return nil
}

func removeDecl(file *dst.File, target dst.Decl) {
	for i, decl := range file.Decls {
		if decl == target {
			file.Decls = append(file.Decls[:i:i], file.Decls[i+1:]...)
			return
		}
	}
}

func removeSpec(decl *dst.GenDecl, target dst.Spec) {
	for i, spec := range decl.Specs {
		if spec == target {
			decl.Specs = append(decl.Specs[:i:i], decl.Specs[i+1:]...)
			return
		}
	}
}

func containsIota(spec *dst.ValueSpec) bool {
	found := false
	dst.Inspect(spec, func(node dst.Node) bool {
		if id, ok := node.(*dst.Ident); ok && id.Name == "iota" {
			found = true
			return false
		}
		return !found
	})
	return found
}

func hasDeclarations(file *dst.File) bool {
	for _, decl := range file.Decls {
		if gen, ok := decl.(*dst.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		return true
	}
	return false
}

func (p *Program) packageFileCount(pkgPath string) int {
	count := 0
	for _, df := range p.files {
		if df.pkg.PkgPath == pkgPath && !strings.HasSuffix(df.path, "_test.go") {
			count++
		}
	}
	return count
}

func (p *Program) packageFilesDeleted(pkgPath string, deleted map[string]bool) bool {
	found := false
	for _, df := range p.files {
		if df.pkg.PkgPath != pkgPath || strings.HasSuffix(df.path, "_test.go") {
			continue
		}
		found = true
		if !deleted[df.path] {
			return false
		}
	}
	return found
}

func uniqueRefs(refs []MoveRef) []MoveRef {
	seen := make(map[MoveRef]bool)
	out := make([]MoveRef, 0, len(refs))
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ImportPath != out[j].ImportPath {
			return out[i].ImportPath < out[j].ImportPath
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func containsRef(refs []MoveRef, want MoveRef) bool {
	for _, ref := range refs {
		if ref == want {
			return true
		}
	}
	return false
}

func uniqueSorted(items []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
