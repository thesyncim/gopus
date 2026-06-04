//nolint:staticcheck // parser object identity is used only as a same-file shadowing safety rail.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const defaultAllowlist = "tools/type_parity_allowlist.tsv"

var guardedTokenRE = regexp.MustCompile(`\bfloat64\b|\bcomplex128\b|\bKissFFT64State\b|\bensureFloat64Slice\b|\bensureComplexSlice\b`)

type rewriteContext uint32

const (
	ctxTypes rewriteContext = 1 << iota
	ctxConversions
	ctxAssignments
	ctxCallArgs
	ctxRSqrt
	ctxSimplifyCasts
)

const ctxAll = ctxTypes | ctxConversions | ctxAssignments | ctxCallArgs | ctxRSqrt | ctxSimplifyCasts

type rule struct {
	pathGlob string
	from     string
	to       string
	contexts rewriteContext
	note     string
	toExpr   ast.Expr
}

type allowEntry struct {
	count  int
	sample string
}

type fileSummary struct {
	path     string
	rewrites int
}

func main() {
	var (
		allowlistPath  string
		rulesPath      string
		write          bool
		emitCandidates bool
		listOnly       bool
	)

	flag.StringVar(&allowlistPath, "allowlist", defaultAllowlist, "type parity allowlist TSV used to restrict rewrites to current guarded findings")
	flag.StringVar(&rulesPath, "rules", "", "rewrite rules TSV: path_glob<TAB>from<TAB>to<TAB>contexts<TAB>note")
	flag.BoolVar(&write, "w", false, "write changed files; default is dry-run")
	flag.BoolVar(&emitCandidates, "emit-candidates", false, "print grouped guarded findings from the allowlist TSV")
	flag.BoolVar(&listOnly, "list", false, "list files that would change without printing a full diff")
	flag.Usage = usage
	flag.Parse()

	allowlist, err := readAllowlist(allowlistPath)
	if err != nil {
		exitf("%v", err)
	}

	if emitCandidates {
		if err := emitCandidateRows(os.Stdout, allowlist); err != nil {
			exitf("%v", err)
		}
		return
	}

	if rulesPath == "" {
		exitf("-rules is required unless -emit-candidates is set")
	}
	rules, err := readRules(rulesPath)
	if err != nil {
		exitf("%v", err)
	}
	if len(rules) == 0 {
		exitf("%s: no rewrite rules", rulesPath)
	}

	files, err := goFiles(flag.Args())
	if err != nil {
		exitf("%v", err)
	}

	var changed []fileSummary
	for _, file := range files {
		matched := matchingRules(file, rules)
		if len(matched) == 0 {
			continue
		}
		summary, diff, err := rewriteFile(file, matched, allowlist[file], write)
		if err != nil {
			exitf("%v", err)
		}
		if summary.rewrites == 0 {
			continue
		}
		changed = append(changed, summary)
		if !write && !listOnly {
			fmt.Print(diff)
		}
	}

	for _, summary := range changed {
		if write {
			fmt.Printf("rewrote %s (%d AST edit(s))\n", summary.path, summary.rewrites)
		} else if listOnly {
			fmt.Printf("would rewrite %s (%d AST edit(s))\n", summary.path, summary.rewrites)
		}
	}
	if len(changed) == 0 {
		fmt.Println("no matching AST rewrites")
	}
}

func usage() {
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), `Usage:
  go run ./tools/typeparityrewrite -emit-candidates
  go run ./tools/typeparityrewrite -rules rewrites.tsv [-w] [paths...]

Rule TSV columns:
  path_glob<TAB>from<TAB>to<TAB>contexts<TAB>note

Contexts are comma-separated:
  types         rewrite type syntax such as []float64, var x float64, make([]float64, n)
  conversions   rewrite conversion calls such as float64(x)
  assignments   wrap assignments into changed scalar/slice vars, e.g. x[i] = float32(expr)
  callargs      wrap same-file calls to functions whose parameter types changed
  rsqrt         rewrite 1/math.Sqrt(x) into a float-width reciprocal sqrt helper
  simplifycasts remove casts between equivalent float-width aliases
  all           all of the above

The allowlist is used as a safety rail: files with allowlist entries are only
edited on current guarded-finding lines. Files absent from the allowlist can be
rewritten by explicit rules, which is useful for tools and tests.

Example rewrites.tsv:
  # path_glob	from	to	contexts	note
  internal/celt/...	float64	celtSig	types,assignments	CELT signal scratch
  tools/gen_opusdec_crossval_fixture.go	float64	float32	types,assignments	crossval PCM

`)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func readAllowlist(name string) (map[string]map[string]allowEntry, error) {
	out := make(map[string]map[string]allowEntry)
	if name == "" {
		return out, nil
	}
	f, err := os.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		parts := strings.SplitN(raw, "\t", 5)
		if len(parts) != 5 {
			return nil, fmt.Errorf("%s:%d: invalid allowlist row", name, lineNo)
		}
		count, err := parsePositiveInt(parts[1])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: invalid count %q", name, lineNo, parts[1])
		}
		pathEntries := out[parts[0]]
		if pathEntries == nil {
			pathEntries = make(map[string]allowEntry)
			out[parts[0]] = pathEntries
		}
		pathEntries[parts[2]] = allowEntry{count: count, sample: unescapeField(parts[4])}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parsePositiveInt(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty int")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid int")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func readRules(name string) ([]rule, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = f.Close()
	}()

	var rules []rule
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		parts := strings.SplitN(raw, "\t", 5)
		if len(parts) < 4 {
			return nil, fmt.Errorf("%s:%d: expected at least 4 TSV columns", name, lineNo)
		}
		toExpr, err := parser.ParseExpr(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: parse target type %q: %w", name, lineNo, parts[2], err)
		}
		contexts, err := parseContexts(parts[3])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, lineNo, err)
		}
		note := ""
		if len(parts) == 5 {
			note = parts[4]
		}
		rules = append(rules, rule{
			pathGlob: parts[0],
			from:     parts[1],
			to:       parts[2],
			contexts: contexts,
			note:     note,
			toExpr:   toExpr,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rules, nil
}

func parseContexts(raw string) (rewriteContext, error) {
	var contexts rewriteContext
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(part) {
		case "types":
			contexts |= ctxTypes
		case "conversions":
			contexts |= ctxConversions
		case "assignments":
			contexts |= ctxAssignments
		case "callargs":
			contexts |= ctxCallArgs
		case "rsqrt":
			contexts |= ctxRSqrt
		case "simplifycasts":
			contexts |= ctxSimplifyCasts
		case "all":
			contexts |= ctxAll
		case "":
		default:
			return 0, fmt.Errorf("unknown context %q", part)
		}
	}
	if contexts == 0 {
		return 0, fmt.Errorf("empty context list")
	}
	return contexts, nil
}

func goFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		cmd := exec.Command("git", "ls-files", "*.go")
		data, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		var files []string
		for _, raw := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if raw != "" {
				files = append(files, filepath.ToSlash(raw))
			}
		}
		return files, nil
	}

	seen := map[string]bool{}
	var files []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if strings.HasSuffix(arg, ".go") {
				clean := cleanGoFilePath(arg)
				if !seen[clean] {
					files = append(files, clean)
					seen[clean] = true
				}
			}
			continue
		}
		err = filepath.WalkDir(arg, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(p, ".go") {
				clean := cleanGoFilePath(p)
				if !seen[clean] {
					files = append(files, clean)
					seen[clean] = true
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func cleanGoFilePath(name string) string {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) {
		if cwd, err := os.Getwd(); err == nil {
			if rel, ok := relUnderDir(cwd, clean); ok {
				clean = rel
			} else if realCwd, cwdErr := filepath.EvalSymlinks(cwd); cwdErr == nil {
				if realClean, cleanErr := filepath.EvalSymlinks(clean); cleanErr == nil {
					if rel, ok := relUnderDir(realCwd, realClean); ok {
						clean = rel
					}
				}
			}
		}
	}
	return filepath.ToSlash(clean)
}

func relUnderDir(dir, name string) (string, bool) {
	rel, err := filepath.Rel(dir, name)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func matchingRules(file string, rules []rule) []rule {
	var out []rule
	for _, rule := range rules {
		if globMatch(rule.pathGlob, file) {
			out = append(out, rule)
		}
	}
	return out
}

func globMatch(pattern, file string) bool {
	pattern = filepath.ToSlash(pattern)
	file = filepath.ToSlash(file)
	if pattern == file || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "/...") {
		prefix := strings.TrimSuffix(pattern, "/...")
		return file == prefix || strings.HasPrefix(file, prefix+"/")
	}
	if strings.Contains(pattern, "...") {
		parts := strings.Split(pattern, "...")
		return strings.HasPrefix(file, parts[0]) && strings.HasSuffix(file, parts[len(parts)-1])
	}
	ok, err := path.Match(pattern, file)
	return err == nil && ok
}

func rewriteFile(file string, rules []rule, allowEntries map[string]allowEntry, write bool) (fileSummary, string, error) {
	original, err := os.ReadFile(file)
	if err != nil {
		return fileSummary{}, "", err
	}

	allowedLines := allowedLineSet(original, allowEntries)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, original, parser.ParseComments)
	if err != nil {
		return fileSummary{}, "", err
	}

	rw := fileRewriter{
		file:              file,
		fset:              fset,
		allowedLines:      allowedLines,
		rules:             rules,
		scalars:           make(map[string]string),
		slices:            make(map[string]string),
		scalarObjects:     make(map[*ast.Object]string),
		sliceObjects:      make(map[*ast.Object]string),
		knownScalars:      make(map[string]string),
		knownSlices:       make(map[string]string),
		knownFieldScalars: make(map[string]string),
		knownFieldSlices:  make(map[string]string),
		knownObjects:      make(map[*ast.Object]string),
		knownSliceObjects: make(map[*ast.Object]string),
		funcArgs:          make(map[string]map[int]string),
	}

	rw.rewriteTypesAndConversions(parsed)
	if rw.hasContext(ctxAssignments) || rw.hasContext(ctxCallArgs) ||
		rw.hasContext(ctxRSqrt) || rw.hasContext(ctxSimplifyCasts) {
		rw.collectKnownValueTypes(parsed)
	}
	if rw.hasContext(ctxAssignments) {
		rw.collectChangedLocals(parsed)
		rw.rewriteAssignments(parsed)
	}
	if rw.hasContext(ctxCallArgs) {
		rw.collectFuncArgs(parsed)
		rw.rewriteCallArgs(parsed)
	}
	if rw.hasContext(ctxRSqrt) {
		rw.rewriteRSqrt(parsed)
	}
	if rw.hasContext(ctxSimplifyCasts) {
		rw.rewriteSimplifyCasts(parsed)
	}

	if rw.rewrites == 0 {
		return fileSummary{path: file}, "", nil
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, parsed); err != nil {
		return fileSummary{}, "", err
	}
	formatted := buf.Bytes()
	if bytes.Equal(original, formatted) {
		return fileSummary{path: file}, "", nil
	}
	if write {
		if err := os.WriteFile(file, formatted, 0o644); err != nil {
			return fileSummary{}, "", err
		}
		return fileSummary{path: file, rewrites: rw.rewrites}, "", nil
	}

	diff := unifiedDiff(file, original, formatted)
	return fileSummary{path: file, rewrites: rw.rewrites}, diff, nil
}

type fileRewriter struct {
	file              string
	fset              *token.FileSet
	allowedLines      map[int]bool
	rules             []rule
	rewrites          int
	scalars           map[string]string
	slices            map[string]string
	scalarObjects     map[*ast.Object]string
	sliceObjects      map[*ast.Object]string
	knownScalars      map[string]string
	knownSlices       map[string]string
	knownFieldScalars map[string]string
	knownFieldSlices  map[string]string
	knownObjects      map[*ast.Object]string
	knownSliceObjects map[*ast.Object]string
	funcArgs          map[string]map[int]string
}

func (rw *fileRewriter) hasContext(ctx rewriteContext) bool {
	for _, rule := range rw.rules {
		if rule.contexts&ctx != 0 {
			return true
		}
	}
	return false
}

func (rw *fileRewriter) rewriteTypesAndConversions(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Field:
			if rw.hasContext(ctxTypes) {
				n.Type = rw.rewriteTypeExpr(n.Type)
			}
		case *ast.TypeSpec:
			if rw.hasContext(ctxTypes) {
				n.Type = rw.rewriteTypeExpr(n.Type)
			}
		case *ast.ValueSpec:
			if rw.hasContext(ctxTypes) {
				n.Type = rw.rewriteTypeExpr(n.Type)
			}
		case *ast.CompositeLit:
			if rw.hasContext(ctxTypes) {
				n.Type = rw.rewriteTypeExpr(n.Type)
			}
		case *ast.TypeAssertExpr:
			if rw.hasContext(ctxTypes) {
				n.Type = rw.rewriteTypeExpr(n.Type)
			}
		case *ast.CallExpr:
			if rw.hasContext(ctxTypes) && isBuiltinTypeArgCall(n) && len(n.Args) > 0 {
				n.Args[0] = rw.rewriteTypeExpr(n.Args[0])
			}
			if rw.hasContext(ctxConversions) {
				for _, rule := range rw.rules {
					if rule.contexts&ctxConversions == 0 || !rw.nodeAllowed(n) {
						continue
					}
					if exprString(rw.fset, n.Fun) == rule.from {
						n.Fun = cloneExprAt(rule.toExpr, n.Fun.Pos())
						rw.rewrites++
						break
					}
				}
			}
		}
		return true
	})
}

func isBuiltinTypeArgCall(call *ast.CallExpr) bool {
	id, ok := call.Fun.(*ast.Ident)
	return ok && (id.Name == "make" || id.Name == "new")
}

func (rw *fileRewriter) rewriteTypeExpr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch expr := expr.(type) {
	case *ast.Ident:
		for _, rule := range rw.rules {
			if rule.contexts&ctxTypes == 0 || rule.from != expr.Name || !rw.nodeAllowed(expr) {
				continue
			}
			rw.rewrites++
			return cloneExprAt(rule.toExpr, expr.Pos())
		}
	case *ast.ArrayType:
		expr.Elt = rw.rewriteTypeExpr(expr.Elt)
	case *ast.ChanType:
		expr.Value = rw.rewriteTypeExpr(expr.Value)
	case *ast.Ellipsis:
		expr.Elt = rw.rewriteTypeExpr(expr.Elt)
	case *ast.FuncType:
		rw.rewriteFieldList(expr.Params)
		rw.rewriteFieldList(expr.Results)
	case *ast.InterfaceType:
		rw.rewriteFieldList(expr.Methods)
	case *ast.MapType:
		expr.Key = rw.rewriteTypeExpr(expr.Key)
		expr.Value = rw.rewriteTypeExpr(expr.Value)
	case *ast.ParenExpr:
		expr.X = rw.rewriteTypeExpr(expr.X)
	case *ast.StarExpr:
		expr.X = rw.rewriteTypeExpr(expr.X)
	case *ast.StructType:
		rw.rewriteFieldList(expr.Fields)
	}
	return expr
}

func (rw *fileRewriter) rewriteFieldList(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		field.Type = rw.rewriteTypeExpr(field.Type)
	}
}

func (rw *fileRewriter) collectChangedLocals(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ValueSpec:
			target := targetScalar(n.Type)
			sliceTarget := targetSlice(n.Type)
			if !rw.isRuleTarget(target) {
				target = ""
			}
			if !rw.isRuleTarget(sliceTarget) {
				sliceTarget = ""
			}
			if target == "" && sliceTarget == "" {
				return true
			}
			for _, name := range n.Names {
				if target != "" {
					rw.recordChangedIdent(name, target, "")
				}
				if sliceTarget != "" {
					rw.recordChangedIdent(name, "", sliceTarget)
				}
			}
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range n.Lhs {
				if i >= len(n.Rhs) {
					continue
				}
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				if target := inferredScalarTarget(n.Rhs[i]); rw.isRuleTarget(target) {
					rw.recordChangedIdent(id, target, "")
				}
				if target := inferredSliceTarget(n.Rhs[i]); rw.isRuleTarget(target) {
					rw.recordChangedIdent(id, "", target)
				}
			}
		case *ast.RangeStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			x, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			target := rw.changedSliceType(x)
			if target == "" {
				return true
			}
			if id, ok := n.Value.(*ast.Ident); ok && id.Name != "_" {
				rw.recordChangedIdent(id, target, "")
			}
		}
		return true
	})
}

func (rw *fileRewriter) isRuleTarget(target string) bool {
	if target == "" {
		return false
	}
	for _, rule := range rw.rules {
		if rule.to == target {
			return true
		}
	}
	return false
}

func (rw *fileRewriter) recordChangedIdent(id *ast.Ident, scalarType, sliceType string) {
	if id == nil || id.Name == "_" {
		return
	}
	if scalarType != "" {
		if id.Obj != nil {
			rw.scalarObjects[id.Obj] = scalarType
		} else {
			rw.scalars[id.Name] = scalarType
		}
	}
	if sliceType != "" {
		if id.Obj != nil {
			rw.sliceObjects[id.Obj] = sliceType
		} else {
			rw.slices[id.Name] = sliceType
		}
	}
}

func (rw *fileRewriter) rewriteAssignments(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			if i >= len(assign.Rhs) || !rw.nodeAllowed(lhs) {
				continue
			}
			target := rw.assignmentTarget(lhs)
			if target == "" || rw.exprAlreadyTargetWidth(assign.Rhs[i], target, true) {
				continue
			}
			assign.Rhs[i] = wrap(target, assign.Rhs[i])
			rw.rewrites++
		}
		return true
	})
}

func (rw *fileRewriter) assignmentTarget(lhs ast.Expr) string {
	switch lhs := lhs.(type) {
	case *ast.Ident:
		return rw.changedScalarType(lhs)
	case *ast.IndexExpr:
		if id, ok := lhs.X.(*ast.Ident); ok {
			return rw.changedSliceType(id)
		}
	}
	return ""
}

func (rw *fileRewriter) changedScalarType(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if id.Obj != nil {
		return rw.scalarObjects[id.Obj]
	}
	return rw.scalars[id.Name]
}

func (rw *fileRewriter) changedSliceType(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if id.Obj != nil {
		return rw.sliceObjects[id.Obj]
	}
	return rw.slices[id.Name]
}

func (rw *fileRewriter) collectFuncArgs(file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Type.Params == nil {
			continue
		}
		argTargets := make(map[int]string)
		argIndex := 0
		for _, field := range fn.Type.Params.List {
			target := targetScalar(field.Type)
			if target == "" {
				target = targetSlice(field.Type)
			}
			if !rw.isRuleTarget(target) {
				target = ""
			}
			names := len(field.Names)
			if names == 0 {
				names = 1
			}
			for i := 0; i < names; i++ {
				if target != "" {
					argTargets[argIndex] = target
				}
				argIndex++
			}
		}
		if len(argTargets) != 0 {
			rw.funcArgs[fn.Name.Name] = argTargets
		}
	}
}

func (rw *fileRewriter) rewriteCallArgs(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		targets := rw.funcArgs[id.Name]
		if len(targets) == 0 {
			return true
		}
		for i, arg := range call.Args {
			target := targets[i]
			if target == "" || rw.exprAlreadyTargetWidth(arg, target, true) || !rw.nodeAllowed(arg) {
				continue
			}
			call.Args[i] = wrap(target, arg)
			rw.rewrites++
		}
		return true
	})
}

func (rw *fileRewriter) collectKnownValueTypes(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			rw.collectKnownFieldTypes(n.Type.Params)
		case *ast.TypeSpec:
			if st, ok := n.Type.(*ast.StructType); ok {
				rw.collectKnownStructFields(st.Fields)
			}
		case *ast.ValueSpec:
			rw.recordKnownNames(n.Names, n.Type)
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			for i, lhs := range n.Lhs {
				if i >= len(n.Rhs) {
					continue
				}
				id, ok := lhs.(*ast.Ident)
				if !ok || id.Name == "_" {
					continue
				}
				if target := inferredScalarTarget(n.Rhs[i]); target != "" {
					rw.recordKnownIdent(id, target, "")
				}
				if target := inferredSliceTarget(n.Rhs[i]); target != "" {
					rw.recordKnownIdent(id, "", target)
				}
			}
		case *ast.RangeStmt:
			if n.Tok != token.DEFINE {
				return true
			}
			x, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			target := rw.knownSliceType(x)
			if target == "" {
				return true
			}
			if id, ok := n.Value.(*ast.Ident); ok && id.Name != "_" {
				rw.recordKnownIdent(id, target, "")
			}
		}
		return true
	})
}

func (rw *fileRewriter) collectKnownStructFields(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		scalarTarget := targetScalar(field.Type)
		sliceTarget := targetSlice(field.Type)
		if scalarTarget == "" && sliceTarget == "" {
			continue
		}
		for _, name := range field.Names {
			if name == nil || name.Name == "_" {
				continue
			}
			if scalarTarget != "" {
				rw.recordKnownField(rw.knownFieldScalars, name.Name, scalarTarget)
			}
			if sliceTarget != "" {
				rw.recordKnownField(rw.knownFieldSlices, name.Name, sliceTarget)
			}
		}
	}
}

func (rw *fileRewriter) recordKnownField(fields map[string]string, name, target string) {
	if prev, ok := fields[name]; ok && prev != target {
		fields[name] = ""
		return
	}
	fields[name] = target
}

func (rw *fileRewriter) collectKnownFieldTypes(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		rw.recordKnownNames(field.Names, field.Type)
	}
}

func (rw *fileRewriter) recordKnownNames(names []*ast.Ident, expr ast.Expr) {
	target := targetScalar(expr)
	sliceTarget := targetSlice(expr)
	for _, name := range names {
		if name == nil || name.Name == "_" {
			continue
		}
		if target != "" {
			rw.recordKnownIdent(name, target, "")
		}
		if sliceTarget != "" {
			rw.recordKnownIdent(name, "", sliceTarget)
		}
	}
}

func (rw *fileRewriter) recordKnownIdent(id *ast.Ident, scalarType, sliceType string) {
	if id == nil || id.Name == "_" {
		return
	}
	if scalarType != "" {
		if id.Obj != nil {
			rw.knownObjects[id.Obj] = scalarType
		} else {
			rw.knownScalars[id.Name] = scalarType
		}
	}
	if sliceType != "" {
		if id.Obj != nil {
			rw.knownSliceObjects[id.Obj] = sliceType
		} else {
			rw.knownSlices[id.Name] = sliceType
		}
	}
}

func (rw *fileRewriter) knownScalarType(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if id.Obj != nil {
		if target := rw.knownObjects[id.Obj]; target != "" {
			return target
		}
	}
	return rw.knownScalars[id.Name]
}

func (rw *fileRewriter) knownSliceType(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	if id.Obj != nil {
		if target := rw.knownSliceObjects[id.Obj]; target != "" {
			return target
		}
	}
	return rw.knownSlices[id.Name]
}

func (rw *fileRewriter) rewriteRSqrt(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				n.Rhs[i] = rw.rewriteRSqrtExpr(rhs)
			}
		case *ast.ValueSpec:
			for i, value := range n.Values {
				n.Values[i] = rw.rewriteRSqrtExpr(value)
			}
		case *ast.ReturnStmt:
			for i, result := range n.Results {
				n.Results[i] = rw.rewriteRSqrtExpr(result)
			}
		case *ast.ExprStmt:
			n.X = rw.rewriteRSqrtExpr(n.X)
		case *ast.IfStmt:
			n.Cond = rw.rewriteRSqrtExpr(n.Cond)
		case *ast.ForStmt:
			n.Cond = rw.rewriteRSqrtExpr(n.Cond)
		case *ast.RangeStmt:
			n.X = rw.rewriteRSqrtExpr(n.X)
		}
		return true
	})
}

func (rw *fileRewriter) rewriteSimplifyCasts(file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				n.Rhs[i] = rw.rewriteSimplifyCastExpr(rhs)
			}
		case *ast.ValueSpec:
			for i, value := range n.Values {
				n.Values[i] = rw.rewriteSimplifyCastExpr(value)
			}
		case *ast.ReturnStmt:
			for i, result := range n.Results {
				n.Results[i] = rw.rewriteSimplifyCastExpr(result)
			}
		case *ast.ExprStmt:
			n.X = rw.rewriteSimplifyCastExpr(n.X)
		case *ast.IfStmt:
			n.Cond = rw.rewriteSimplifyCastExpr(n.Cond)
		case *ast.ForStmt:
			n.Cond = rw.rewriteSimplifyCastExpr(n.Cond)
		case *ast.RangeStmt:
			n.X = rw.rewriteSimplifyCastExpr(n.X)
		}
		return true
	})
}

func (rw *fileRewriter) rewriteRSqrtExpr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		expr.X = rw.rewriteRSqrtExpr(expr.X)
		expr.Y = rw.rewriteRSqrtExpr(expr.Y)
		if expr.Op != token.QUO || !isOneLiteral(expr.X) || !rw.nodeAllowed(expr) {
			return expr
		}
		call, ok := expr.Y.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return expr
		}
		for _, rule := range rw.rules {
			if rule.contexts&ctxRSqrt == 0 || exprString(rw.fset, call.Fun) != rule.from {
				continue
			}
			rw.rewrites++
			return &ast.CallExpr{
				Fun:  cloneExprAt(rule.toExpr, expr.Pos()),
				Args: []ast.Expr{rw.float32Arg(call.Args[0])},
			}
		}
	case *ast.CallExpr:
		for i, arg := range expr.Args {
			expr.Args[i] = rw.rewriteRSqrtExpr(arg)
		}
	case *ast.CompositeLit:
		for i, elt := range expr.Elts {
			expr.Elts[i] = rw.rewriteRSqrtExpr(elt)
		}
	case *ast.IndexExpr:
		expr.X = rw.rewriteRSqrtExpr(expr.X)
		expr.Index = rw.rewriteRSqrtExpr(expr.Index)
	case *ast.KeyValueExpr:
		expr.Value = rw.rewriteRSqrtExpr(expr.Value)
	case *ast.ParenExpr:
		expr.X = rw.rewriteRSqrtExpr(expr.X)
	case *ast.SliceExpr:
		expr.X = rw.rewriteRSqrtExpr(expr.X)
		expr.Low = rw.rewriteRSqrtExpr(expr.Low)
		expr.High = rw.rewriteRSqrtExpr(expr.High)
		expr.Max = rw.rewriteRSqrtExpr(expr.Max)
	case *ast.StarExpr:
		expr.X = rw.rewriteRSqrtExpr(expr.X)
	case *ast.UnaryExpr:
		expr.X = rw.rewriteRSqrtExpr(expr.X)
	}
	return expr
}

func (rw *fileRewriter) rewriteSimplifyCastExpr(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch expr := expr.(type) {
	case *ast.BinaryExpr:
		expr.X = rw.rewriteSimplifyCastExpr(expr.X)
		expr.Y = rw.rewriteSimplifyCastExpr(expr.Y)
	case *ast.CallExpr:
		for i, arg := range expr.Args {
			expr.Args[i] = rw.rewriteSimplifyCastExpr(arg)
		}
		if len(expr.Args) == 1 && isFloat32WidthType(exprString(rw.fset, expr.Fun)) &&
			rw.exprAlreadyFloat32Width(expr.Args[0], false) && rw.nodeAllowed(expr) {
			rw.rewrites++
			return expr.Args[0]
		}
	case *ast.CompositeLit:
		for i, elt := range expr.Elts {
			expr.Elts[i] = rw.rewriteSimplifyCastExpr(elt)
		}
	case *ast.IndexExpr:
		expr.X = rw.rewriteSimplifyCastExpr(expr.X)
		expr.Index = rw.rewriteSimplifyCastExpr(expr.Index)
	case *ast.KeyValueExpr:
		expr.Value = rw.rewriteSimplifyCastExpr(expr.Value)
	case *ast.ParenExpr:
		expr.X = rw.rewriteSimplifyCastExpr(expr.X)
	case *ast.SliceExpr:
		expr.X = rw.rewriteSimplifyCastExpr(expr.X)
		expr.Low = rw.rewriteSimplifyCastExpr(expr.Low)
		expr.High = rw.rewriteSimplifyCastExpr(expr.High)
		expr.Max = rw.rewriteSimplifyCastExpr(expr.Max)
	case *ast.StarExpr:
		expr.X = rw.rewriteSimplifyCastExpr(expr.X)
	case *ast.UnaryExpr:
		expr.X = rw.rewriteSimplifyCastExpr(expr.X)
	}
	return expr
}

func isOneLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	if !ok {
		return false
	}
	return lit.Value == "1" || lit.Value == "1.0" || lit.Value == "1."
}

func (rw *fileRewriter) float32Arg(expr ast.Expr) ast.Expr {
	if rw.exprAlreadyFloat32Width(expr, true) {
		return expr
	}
	return wrap("float32", expr)
}

func (rw *fileRewriter) exprAlreadyFloat32Width(expr ast.Expr, allowUntypedConstant bool) bool {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		return allowUntypedConstant
	case *ast.BinaryExpr:
		left := rw.exprAlreadyFloat32Width(expr.X, allowUntypedConstant)
		right := rw.exprAlreadyFloat32Width(expr.Y, allowUntypedConstant)
		if !left || !right {
			return false
		}
		if allowUntypedConstant {
			return true
		}
		return rw.exprAlreadyFloat32Width(expr.X, false) || rw.exprAlreadyFloat32Width(expr.Y, false)
	case *ast.CallExpr:
		return isFloat32WidthType(exprString(token.NewFileSet(), expr.Fun))
	case *ast.Ident:
		return isFloat32WidthType(rw.scalarType(expr))
	case *ast.IndexExpr:
		return isFloat32WidthType(rw.sliceElementType(expr.X))
	case *ast.ParenExpr:
		return rw.exprAlreadyFloat32Width(expr.X, allowUntypedConstant)
	case *ast.SelectorExpr:
		return isFloat32WidthType(rw.knownFieldScalars[expr.Sel.Name])
	case *ast.UnaryExpr:
		return rw.exprAlreadyFloat32Width(expr.X, allowUntypedConstant)
	}
	return false
}

func (rw *fileRewriter) exprAlreadyTargetWidth(expr ast.Expr, target string, allowUntypedConstant bool) bool {
	if exprAlreadyTarget(expr, target) {
		return true
	}
	return isFloat32WidthType(target) && rw.exprAlreadyFloat32Width(expr, allowUntypedConstant)
}

func (rw *fileRewriter) scalarType(id *ast.Ident) string {
	if target := rw.knownScalarType(id); target != "" {
		return target
	}
	return rw.changedScalarType(id)
}

func (rw *fileRewriter) sliceElementType(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		if target := rw.knownSliceType(expr); target != "" {
			return target
		}
		return rw.changedSliceType(expr)
	case *ast.SelectorExpr:
		return rw.knownFieldSlices[expr.Sel.Name]
	}
	return ""
}

func isFloat32WidthType(name string) bool {
	switch name {
	case "float32", "celtNorm", "celtSig", "celtEner", "celtGLog", "opusVal16", "opusVal32", "opusRes":
		return true
	default:
		return false
	}
}

func (rw *fileRewriter) nodeAllowed(n ast.Node) bool {
	if len(rw.allowedLines) == 0 {
		return true
	}
	line := rw.fset.Position(n.Pos()).Line
	return rw.allowedLines[line]
}

func targetScalar(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.SelectorExpr:
		return exprString(token.NewFileSet(), expr)
	}
	return ""
}

func targetSlice(expr ast.Expr) string {
	arr, ok := expr.(*ast.ArrayType)
	if !ok {
		return ""
	}
	return targetScalar(arr.Elt)
}

func inferredScalarTarget(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	if len(call.Args) == 1 {
		return exprString(token.NewFileSet(), call.Fun)
	}
	return ""
}

func inferredSliceTarget(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.CallExpr:
		if isBuiltinTypeArgCall(expr) && len(expr.Args) > 0 {
			return targetSlice(expr.Args[0])
		}
	case *ast.CompositeLit:
		return targetSlice(expr.Type)
	}
	return ""
}

func exprAlreadyTarget(expr ast.Expr, target string) bool {
	call, ok := expr.(*ast.CallExpr)
	return ok && exprString(token.NewFileSet(), call.Fun) == target
}

func wrap(target string, expr ast.Expr) ast.Expr {
	targetExpr, err := parser.ParseExpr(target)
	if err != nil {
		targetExpr = ast.NewIdent(target)
	}
	return &ast.CallExpr{
		Fun:  targetExpr,
		Args: []ast.Expr{expr},
	}
}

func allowedLineSet(src []byte, entries map[string]allowEntry) map[int]bool {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[int]bool)
	remaining := make(map[string]int, len(entries))
	for digest, entry := range entries {
		remaining[digest] = entry.count
	}
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		digest := digestLine(line)
		if remaining[digest] > 0 {
			out[i+1] = true
			remaining[digest]--
		}
	}
	return out
}

func digestLine(line string) string {
	sum := sha256.Sum256([]byte(normalizedLine(line)))
	return fmt.Sprintf("%x", sum[:])
}

func normalizedLine(line string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
}

func cloneExpr(expr ast.Expr) ast.Expr {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, token.NewFileSet(), expr)
	clone, err := parser.ParseExpr(buf.String())
	if err != nil {
		return ast.NewIdent(buf.String())
	}
	return clone
}

func cloneExprAt(expr ast.Expr, pos token.Pos) ast.Expr {
	if id, ok := expr.(*ast.Ident); ok {
		return &ast.Ident{NamePos: pos, Name: id.Name}
	}
	return cloneExpr(expr)
}

func exprString(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	_ = printer.Fprint(&buf, fset, expr)
	return buf.String()
}

func emitCandidateRows(w io.Writer, allowlist map[string]map[string]allowEntry) error {
	type row struct {
		path   string
		token  string
		count  int
		sample string
	}
	var rows []row
	for p, entries := range allowlist {
		for _, entry := range entries {
			token := guardedTokenRE.FindString(entry.sample)
			if token == "" {
				continue
			}
			rows = append(rows, row{
				path:   p,
				token:  token,
				count:  entry.count,
				sample: entry.sample,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].path != rows[j].path {
			return rows[i].path < rows[j].path
		}
		if rows[i].token != rows[j].token {
			return rows[i].token < rows[j].token
		}
		return rows[i].sample < rows[j].sample
	})
	_, err := fmt.Fprintln(w, "path\ttoken\tcount\tsample")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", row.path, row.token, row.count, row.sample); err != nil {
			return err
		}
	}
	return nil
}

func unifiedDiff(file string, before, after []byte) string {
	tmp, err := os.MkdirTemp("", "gopus-typeparityrewrite-*")
	if err != nil {
		return fmt.Sprintf("--- %s\n+++ %s\n(binary diff unavailable: %v)\n", file, file, err)
	}
	defer func() {
		_ = os.RemoveAll(tmp)
	}()

	beforePath := filepath.Join(tmp, "before")
	afterPath := filepath.Join(tmp, "after")
	if err := os.WriteFile(beforePath, before, 0o644); err != nil {
		return fmt.Sprintf("--- %s\n+++ %s\n(diff unavailable: %v)\n", file, file, err)
	}
	if err := os.WriteFile(afterPath, after, 0o644); err != nil {
		return fmt.Sprintf("--- %s\n+++ %s\n(diff unavailable: %v)\n", file, file, err)
	}
	cmd := exec.Command("diff", "-u", "--label", file+" (before)", "--label", file+" (after)", beforePath, afterPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			return string(out)
		}
		return fmt.Sprintf("--- %s\n+++ %s\n(diff unavailable: %v)\n", file, file, err)
	}
	return string(out)
}

func unescapeField(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(value, `\\`, `\`), `\t`, "\t"), `\n`, "\n")
}
