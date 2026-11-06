// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"strconv"
	"strings"
)

// trackingScan is deliberately conservative: proofs belong to a transaction,
// page identity and lexical control-flow path. Branch/loop-local proofs never
// authorize later writes. Helpers that depend on a caller use RequireTracked.
// This complements the transaction-handle scan, not Go's type checker.
type trackingScan struct {
	set         *token.FileSet
	pkg         string
	body        *ast.BlockStmt
	diagnostics *[]string
}

type trackingState struct {
	values  map[string]string
	tracked map[string]bool
	pending map[string]string   // created page identity -> transaction
	checks  map[string][]string // error variable -> proofs released on success
}

func (s trackingState) clone() trackingState {
	return trackingState{maps.Clone(s.values), maps.Clone(s.tracked), maps.Clone(s.pending), maps.Clone(s.checks)}
}

func trackedWriterDiagnostics(set *token.FileSet, file *ast.File, fn *ast.FuncDecl) []string {
	var diagnostics []string
	scan := trackingScan{set, file.Name.Name, fn.Body, &diagnostics}
	state := trackingState{make(map[string]string), make(map[string]bool), make(map[string]string), make(map[string][]string)}
	scan.parameters(fn.Type.Params, state)
	scan.parameters(fn.Recv, state)
	scan.routine(fn.Body, state)
	return diagnostics
}

func (s trackingScan) report(node ast.Node, format string, args ...any) {
	*s.diagnostics = append(*s.diagnostics, fmt.Sprintf("%s: %s", s.set.Position(node.Pos()), fmt.Sprintf(format, args...)))
}

func trackingName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		if value.Obj != nil {
			return fmt.Sprintf("%s@%d", value.Name, value.Obj.Pos())
		}
		return value.Name
	case *ast.SelectorExpr:
		return trackingName(value.X) + "." + value.Sel.Name
	case *ast.ParenExpr:
		return trackingName(value.X)
	}
	return fmt.Sprintf("expression@%d", expr.Pos())
}

func (st trackingState) value(expr ast.Expr) string {
	if literal, ok := expr.(*ast.BasicLit); ok {
		return literal.Value
	}
	name := trackingName(expr)
	if resolved, ok := st.values[name]; ok {
		return resolved
	}
	if selected, ok := expr.(*ast.SelectorExpr); ok {
		base := st.value(selected.X)
		if selected.Sel.Name == "Q" && len(base) > 3 && base[:3] == "tx:" {
			return "q:" + base
		}
		return base + "." + selected.Sel.Name
	}
	return name
}

func (s trackingScan) parameters(fields *ast.FieldList, st trackingState) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			st.values[trackingName(name)] = trackingName(name)
		}
		pointer, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		isTx := false
		switch typ := pointer.X.(type) {
		case *ast.SelectorExpr:
			pkg, ok := typ.X.(*ast.Ident)
			isTx = ok && pkg.Name == "pagewrite" && typ.Sel.Name == "Tx"
		case *ast.Ident:
			isTx = s.pkg == "pagewrite" && typ.Name == "Tx"
		}
		if isTx {
			for _, name := range field.Names {
				st.values[trackingName(name)] = "tx:" + trackingName(name)
			}
		}
	}
}

func trackingMethod(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

func (st trackingState) transaction(call *ast.CallExpr) string {
	selected, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	value := st.value(selected.X)
	if len(value) > 2 && value[:2] == "q:" {
		return value[2:]
	}
	if len(value) > 3 && value[:3] == "tx:" {
		return value
	}
	return ""
}

func (st trackingState) field(expr ast.Expr, name string) string {
	if literal, ok := expr.(*ast.CompositeLit); ok {
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if key, isName := pairKey(pair); ok && isName && key == name {
				return st.value(pair.Value)
			}
		}
	}
	key := trackingName(expr) + "." + name
	if value, ok := st.values[key]; ok {
		return value
	}
	return st.value(expr) + "." + name
}

func pairKey(pair *ast.KeyValueExpr) (string, bool) {
	if pair == nil {
		return "", false
	}
	key, ok := pair.Key.(*ast.Ident)
	if !ok {
		return "", false
	}
	return key.Name, true
}

func trackingProof(transaction, id string) string { return transaction + "/" + id }

func (s trackingScan) require(call *ast.CallExpr, st trackingState, transaction, id string) {
	if !st.tracked[trackingProof(transaction, id)] {
		s.report(call, "%s needs prior tracking of %s in %s", trackingMethod(call), id, transaction)
	}
}

func (s trackingScan) expression(expr ast.Expr, st trackingState) {
	if expr == nil {
		return
	}
	ast.Inspect(expr, func(node ast.Node) bool {
		if closure, ok := node.(*ast.FuncLit); ok {
			nested := st.clone()
			nested.pending = make(map[string]string)
			s.parameters(closure.Type.Params, nested)
			s.routine(closure.Body, nested)
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		method, transaction := trackingMethod(call), st.transaction(call)
		if method == "Insert" && len(call.Args) == 5 && s.isInsert(call) {
			if callback, ok := call.Args[4].(*ast.FuncLit); ok && len(callback.Type.Params.List) == 2 {
				nested := st.clone()
				nested.pending = make(map[string]string)
				s.parameters(callback.Type.Params, nested)
				work, page := callback.Type.Params.List[0].Names[0], callback.Type.Params.List[1].Names[0]
				nested.tracked[trackingProof(nested.value(work), nested.field(page, "ID"))] = true
				s.routine(callback.Body, nested)
				for _, argument := range call.Args[:4] {
					s.expression(argument, st)
				}
				return false
			}
		}
		if transaction == "" {
			return true
		}
		if trackingChecksError(method) && call != expr {
			s.report(call, "tracking must be a direct call with its error checked")
			return true
		}
		switch method {
		case "CreatePage":
			if st.values[fmt.Sprintf("assigned-create@%d", call.Pos())] != "yes" {
				s.report(call, "CreatePage result must be assigned and registered with Created")
			}
		case "TrackMedia":
			if len(call.Args) == 2 {
				st.tracked[trackingProof(transaction, "media:"+st.value(call.Args[1]))] = true
			}
		case "RequireTrackedEntity":
			if len(call.Args) > 1 {
				for _, id := range call.Args[1:] {
					st.tracked[trackingProof(transaction, st.value(call.Args[0])+":"+st.value(id))] = true
				}
			}
		case "Track", "TrackSystem", "TrackComponent", "RequireTracked":
			argument := 1
			if method == "RequireTracked" {
				argument = 0
			}
			if len(call.Args) > argument {
				st.tracked[trackingProof(transaction, st.value(call.Args[argument]))] = true
			}
		case "Created":
			if len(call.Args) == 1 {
				id := st.field(call.Args[0], "ID")
				if st.pending[trackingProof(transaction, id)] != transaction {
					s.report(call, "Created must register this transaction's newly inserted page")
					return true
				}
				st.tracked[trackingProof(transaction, id)] = true
				delete(st.pending, trackingProof(transaction, id))
			}
		default:
			if !pageMutations[method] || len(call.Args) != 2 || nonPageTranslation(call) || method == "CreatePage" {
				return true
			}
			id := st.value(call.Args[1])
			switch method {
			case "UpdatePage", "UpdatePageCreatedAt", "PublishPage", "PublishUnpublishedPage", "UnpublishPage", "PublishScheduledPage", "ClearPageScheduledAt", "UpdatePageFeaturedImage", "ClearPageFeaturedImage", "UpdatePageLanguage":
				id = st.field(call.Args[1], "ID")
			case "SetPageAIAccess", "AddTagToPage", "RemoveTagFromPage", "AddCategoryToPage", "RemoveCategoryFromPage", "CreatePageAlias":
				id = st.field(call.Args[1], "PageID")
			case "DeleteMedia":
				id = "media:" + id
			case "DeletePageAlias":
				id = st.values["alias:"+transaction+":"+id]
			case "DeleteTag", "DeleteCategory":
				if s.taxonomyTracked(call, method, st) {
					return true
				}
			case "CreateTranslation", "DeleteTranslationsForEntity", "DeleteTranslationsRelatedToEntity", "DeleteTranslationsForEntityAndLanguage":
				kind := st.field(call.Args[1], "EntityType")
				for _, field := range []string{"EntityID", "TranslationID"} {
					if field == "TranslationID" && (method == "DeleteTranslationsForEntity" || method == "DeleteTranslationsForEntityAndLanguage") {
						continue
					}
					s.requireEntity(call, st, transaction, kind, st.field(call.Args[1], field))
				}
				return true
			case "DeleteTranslation":
				key := "edge:" + transaction + ":" + id
				s.requireEntity(call, st, transaction, st.values[key+".kind"], st.values[key+".source"])
				s.requireEntity(call, st, transaction, st.values[key+".kind"], st.values[key+".target"])
				return true
			}
			s.require(call, st, transaction, id)
		}
		return true
	})
}

func (s trackingScan) isInsert(call *ast.CallExpr) bool {
	if selected, ok := call.Fun.(*ast.SelectorExpr); ok {
		return trackingName(selected.X) == "pagewrite"
	}
	_, local := call.Fun.(*ast.Ident)
	return local && s.pkg == "pagewrite"
}

func (s trackingScan) requireEntity(call *ast.CallExpr, st trackingState, transaction, kind, id string) {
	if kind == `"tag"` || kind == `"category"` || kind == `"form"` || st.tracked[trackingProof(transaction, kind+":"+id)] {
		return
	}
	if kind != `"page"` || !st.tracked[trackingProof(transaction, id)] {
		s.report(call, "%s needs prior tracking of %s entity %s in %s", trackingMethod(call), kind, id, transaction)
	}
}

// Deleting a term mutates a set of pages rather than the term ID itself.
// Require the matching lookup and a preceding complete range over that result,
// with its first statement tracking each page. The ordinary scan separately
// checks each unlink in that range. New cascade shapes fail closed.
func (s trackingScan) taxonomyTracked(mutation *ast.CallExpr, method string, st trackingState) bool {
	lookup := "ListPageIDsFor" + strings.TrimPrefix(method, "Delete")
	var listed string
	var proven bool
	ast.Inspect(s.body, func(node ast.Node) bool {
		if node == nil || node.Pos() >= mutation.Pos() {
			return false
		}
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if assign, ok := node.(*ast.AssignStmt); ok && len(assign.Rhs) == 1 && len(assign.Lhs) > 0 {
			if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && trackingMethod(call) == lookup && len(call.Args) == 2 && st.transaction(call) == st.transaction(mutation) && st.value(call.Args[1]) == st.value(mutation.Args[1]) {
				listed = trackingName(assign.Lhs[0])
			}
		}
		if loop, ok := node.(*ast.RangeStmt); ok && loop.End() < mutation.Pos() && loop.Value != nil && trackingName(loop.X) == listed && len(loop.Body.List) > 0 && s.topLevel(loop) {
			if first, ok := loop.Body.List[0].(*ast.IfStmt); ok {
				if assign, ok := first.Init.(*ast.AssignStmt); ok && len(assign.Rhs) == 1 {
					if call, ok := assign.Rhs[0].(*ast.CallExpr); ok && trackingMethod(call) == "TrackSystem" && len(call.Args) == 2 && st.transaction(call) == st.transaction(mutation) && trackingName(call.Args[1]) == trackingName(loop.Value) && trackingTerminates(first.Body) && trackingCheckedError(first.Cond) == trackingName(assign.Lhs[len(assign.Lhs)-1]) {
						proven = true
					}
				}
			}
		}
		return true
	})
	return proven
}

func (s trackingScan) topLevel(statement ast.Stmt) bool {
	for _, candidate := range s.body.List {
		if candidate == statement {
			return true
		}
	}
	return false
}

func (s trackingScan) assign(lhs, rhs ast.Expr, st trackingState) {
	if selected, ok := rhs.(*ast.SelectorExpr); ok && pageMutations[selected.Sel.Name] && strings.HasPrefix(st.value(selected.X), "q:") {
		s.report(selected, "page mutation method values need an explicit tracked call")
	}
	name := trackingName(lhs)
	for field := range st.values {
		if strings.HasPrefix(field, name+".") {
			delete(st.values, field)
		}
	}
	st.values[name] = st.value(rhs)
	if literal, ok := rhs.(*ast.CompositeLit); ok {
		for _, element := range literal.Elts {
			if pair, ok := element.(*ast.KeyValueExpr); ok {
				if key, ok := pairKey(pair); ok {
					st.values[name+"."+key] = st.value(pair.Value)
				}
			}
		}
	}
	call, ok := rhs.(*ast.CallExpr)
	if !ok {
		return
	}
	switch trackingMethod(call) {
	case "GetPageAliasByID":
		if len(call.Args) == 2 {
			st.values["alias:"+st.transaction(call)+":"+st.value(call.Args[1])] = st.field(lhs, "PageID")
		}
	case "GetTranslationByID":
		if len(call.Args) == 2 {
			key := "edge:" + st.transaction(call) + ":" + st.value(call.Args[1])
			st.values[key+".kind"] = st.field(lhs, "EntityType")
			st.values[key+".source"] = st.field(lhs, "EntityID")
			st.values[key+".target"] = st.field(lhs, "TranslationID")
		}
	case "NewTx":
		transaction := fmt.Sprintf("tx:%s:new@%d", name, call.Pos())
		st.values[name] = transaction
		if len(call.Args) > 0 {
			st.values[trackingName(call.Args[0])] = "q:" + transaction
		}
	case "GetPageByID", "Track", "TrackSystem":
		if len(call.Args) > 1 {
			st.values[name+".ID"] = st.value(call.Args[1])
		}
	case "ParamsFor":
		if len(call.Args) == 1 {
			st.values[name+".ID"] = st.field(call.Args[0], "ID")
		}
	case "UpdatePage":
		if len(call.Args) == 2 {
			st.values[name+".ID"] = st.field(call.Args[1], "ID")
		}
	case "CreatePage":
		if transaction := st.transaction(call); transaction != "" {
			id := "created@" + strconv.Itoa(int(call.Pos()))
			st.values[name+".ID"] = id
			st.pending[trackingProof(transaction, id)] = transaction
		}
	}
}

// Statements are visited in execution order. Only an if's initializer is
// unconditional; its body, else and every loop get separate proof scopes.
func (s trackingScan) routine(body *ast.BlockStmt, st trackingState) {
	s.block(body, st, "")
	if len(st.pending) > 0 && (len(body.List) == 0 || !trackingTerminates(body)) {
		s.report(body, "created page must be registered with Created before function exit")
	}
}

func (s trackingScan) block(body *ast.BlockStmt, st trackingState, failure string) {
	for _, statement := range body.List {
		s.statement(statement, st, failure)
	}
}

func (s trackingScan) statement(statement ast.Stmt, st trackingState, failure string) {
	switch value := statement.(type) {
	case *ast.AssignStmt:
		for _, rhs := range value.Rhs {
			before := maps.Clone(st.tracked)
			s.assignedExpression(rhs, st)
			if call, ok := rhs.(*ast.CallExpr); ok && trackingChecksError(trackingMethod(call)) && len(value.Lhs) > 0 {
				var proofs []string
				for proof := range st.tracked {
					if !before[proof] {
						proofs = append(proofs, proof)
						delete(st.tracked, proof)
					}
				}
				st.checks[trackingName(value.Lhs[len(value.Lhs)-1])] = proofs
			}
		}
		for i, lhs := range value.Lhs {
			if i < len(value.Rhs) {
				if value.Tok == token.ASSIGN || value.Tok == token.DEFINE {
					s.assign(lhs, value.Rhs[i], st)
				} else {
					st.values[trackingName(lhs)] = fmt.Sprintf("modified@%d", value.Pos())
				}
			}
		}
	case *ast.IncDecStmt:
		st.values[trackingName(value.X)] = fmt.Sprintf("modified@%d", value.Pos())
	case *ast.SendStmt:
		s.expression(value.Chan, st)
		s.expression(value.Value, st)
	case *ast.ExprStmt:
		if call, ok := value.X.(*ast.CallExpr); ok && st.transaction(call) != "" && trackingChecksError(trackingMethod(call)) {
			s.report(call, "tracking error must be checked before mutation")
		} else {
			s.expression(value.X, st)
		}
	case *ast.ReturnStmt:
		for _, result := range value.Results {
			s.expression(result, st)
		}
		if len(st.pending) > 0 && !returnsTrackingError(value, failure) {
			s.report(value, "created page must be registered with Created before returning")
		}
	case *ast.IfStmt:
		s.statement(value.Init, st, failure)
		s.expression(value.Cond, st.clone())
		thenState, elseState := st.clone(), st.clone()
		branchFailure := failure
		if checked := trackingCheckedError(value.Cond); checked != "" {
			branchFailure = checked
		}
		s.block(value.Body, thenState, branchFailure)
		if value.Else != nil {
			s.statement(value.Else, elseState, failure)
		}
		if trackingTerminates(value.Body) {
			if checked := trackingCheckedError(value.Cond); checked != "" {
				for _, proof := range st.checks[checked] {
					st.tracked[proof] = true
				}
			}
		} else {
			invalidateTrackingValues(st, thenState, value.Pos())
		}
		invalidateTrackingValues(st, elseState, value.Pos())
		if condition, ok := value.Cond.(*ast.BinaryExpr); ok && condition.Op == token.EQL && st.value(condition.Y) == `"page"` {
			for proof := range thenState.tracked {
				transaction, id, _ := strings.Cut(proof, "/")
				st.tracked[trackingProof(transaction, st.value(condition.X)+":"+id)] = true
			}
		}
	case *ast.BlockStmt:
		s.block(value, st, failure)
	case *ast.RangeStmt:
		s.expression(value.X, st)
		nested := st.clone()
		for _, variable := range []ast.Expr{value.Key, value.Value} {
			if variable != nil {
				nested.values[trackingName(variable)] = fmt.Sprintf("range@%d:%s", value.Pos(), trackingName(variable))
			}
		}
		s.block(value.Body, nested, failure)
		if items, ok := value.X.(*ast.CompositeLit); ok && value.Value != nil {
			for proof := range nested.tracked {
				transaction, id, _ := strings.Cut(proof, "/")
				if id == nested.value(value.Value) {
					for _, item := range items.Elts {
						st.tracked[trackingProof(transaction, st.value(item))] = true
					}
				}
			}
		}
		invalidateTrackingValues(st, nested, value.Pos())
	case *ast.ForStmt:
		nested := st.clone()
		s.statement(value.Init, nested, failure)
		s.expression(value.Cond, nested.clone())
		s.block(value.Body, nested, failure)
		s.statement(value.Post, nested, failure)
		invalidateTrackingValues(st, nested, value.Pos())
	case *ast.SwitchStmt:
		s.statement(value.Init, st, failure)
		s.expression(value.Tag, st)
		for _, clause := range value.Body.List {
			branch := clause.(*ast.CaseClause)
			nested := st.clone()
			s.block(&ast.BlockStmt{List: branch.Body}, nested, failure)
			invalidateTrackingValues(st, nested, value.Pos())
		}
	case *ast.TypeSwitchStmt:
		s.statement(value.Init, st, failure)
		s.statement(value.Assign, st, failure)
		for _, clause := range value.Body.List {
			branch := clause.(*ast.CaseClause)
			nested := st.clone()
			s.block(&ast.BlockStmt{List: branch.Body}, nested, failure)
			invalidateTrackingValues(st, nested, value.Pos())
		}
	case *ast.SelectStmt:
		for _, clause := range value.Body.List {
			branch := clause.(*ast.CommClause)
			nested := st.clone()
			s.statement(branch.Comm, nested, failure)
			s.block(&ast.BlockStmt{List: branch.Body}, nested, failure)
			invalidateTrackingValues(st, nested, value.Pos())
		}
	case *ast.GoStmt:
		s.asynchronous(value.Call, st)
	case *ast.DeferStmt:
		s.asynchronous(value.Call, st)
	case *ast.LabeledStmt:
		s.statement(value.Stmt, st, failure)
	case *ast.BranchStmt:
		if value.Tok == token.GOTO {
			for _, binding := range st.values {
				if strings.HasPrefix(binding, "tx:") {
					s.report(value, "goto cannot establish page tracking order")
					break
				}
			}
		}
	case *ast.DeclStmt:
		if declaration, ok := value.Decl.(*ast.GenDecl); ok {
			for _, spec := range declaration.Specs {
				if values, ok := spec.(*ast.ValueSpec); ok {
					for i, expr := range values.Values {
						s.assignedExpression(expr, st)
						if i < len(values.Names) {
							s.assign(values.Names[i], expr, st)
						}
					}
				}
			}
		}
	}
}

func (s trackingScan) assignedExpression(expr ast.Expr, st trackingState) {
	key := fmt.Sprintf("assigned-create@%d", expr.Pos())
	st.values[key] = "yes"
	s.expression(expr, st)
	delete(st.values, key)
}

func returnsTrackingError(statement *ast.ReturnStmt, checked string) bool {
	if checked == "" {
		return false
	}
	returned := false
	for _, result := range statement.Results {
		ast.Inspect(result, func(node ast.Node) bool {
			if name, ok := node.(*ast.Ident); ok && trackingName(name) == checked {
				returned = true
			}
			return true
		})
	}
	return returned
}

func (s trackingScan) asynchronous(call *ast.CallExpr, st trackingState) {
	ast.Inspect(call, func(node ast.Node) bool {
		if mutation, ok := node.(*ast.CallExpr); ok && pageMutations[trackingMethod(mutation)] {
			s.report(mutation, "page mutations must run synchronously before Finish")
		}
		return true
	})
	s.expression(call, st.clone())
}

func invalidateTrackingValues(st, branch trackingState, position token.Pos) {
	for name, changed := range branch.values {
		if st.values[name] != changed {
			st.values[name] = fmt.Sprintf("join@%d:%s", position, name)
		}
	}
}

func trackingChecksError(method string) bool {
	switch method {
	case "Track", "TrackSystem", "TrackComponent", "TrackMedia", "RequireTracked", "RequireTrackedEntity":
		return true
	}
	return false
}

func trackingTerminates(body *ast.BlockStmt) bool {
	if len(body.List) == 0 {
		return false
	}
	switch body.List[len(body.List)-1].(type) {
	case *ast.ReturnStmt, *ast.BranchStmt:
		return true
	}
	return false
}

func trackingCheckedError(expr ast.Expr) string {
	comparison, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return ""
	}
	if comparison.Op == token.LAND {
		return trackingCheckedError(comparison.X)
	}
	if comparison.Op == token.NEQ {
		if nilValue, ok := comparison.Y.(*ast.Ident); ok && nilValue.Name == "nil" {
			return trackingName(comparison.X)
		}
	}
	return ""
}
