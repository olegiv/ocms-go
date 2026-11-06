// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package pagewrite_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestWriterTrackingFollowsPageAndTransaction(t *testing.T) {
	const track = `if _, err := work.Track(ctx, id, nil, true); err != nil { return err }; `
	const mutate = `return work.Q.ClearPageTags(ctx, id)`
	const terms = `ids, err := work.Q.ListPageIDsForTag(ctx,id); if err!=nil{return err}; `
	const trackTerms = `for _,pageID := range ids { if _,err:=work.TrackSystem(ctx,pageID);err!=nil{return err}; if err:=work.Q.RemoveTagFromPage(ctx,store.RemoveTagFromPageParams{PageID:pageID,TagID:id});err!=nil{return err} }; `
	for _, tc := range []struct {
		name        string
		body        string
		wantFailure bool
	}{
		{"tracked", track + mutate, false},
		{"untracked", mutate, true},
		{"late tracking", `err := work.Q.ClearPageTags(ctx, id); ` + track + `return err`, true},
		{"wrong page", track + `return work.Q.ClearPageTags(ctx, otherID)`, true},
		{"mutation method value", track + `write:=work.Q.ClearPageTags; return write(ctx,id)`, true},
		{"deferred mutation", track + `defer work.Q.ClearPageTags(ctx,id); return nil`, true},
		{"goroutine mutation", track + `go work.Q.ClearPageTags(ctx,id); return nil`, true},
		{"short circuit check", `if flag && work.RequireTracked(id)==nil {}; ` + mutate, true},
		{"tracking as an argument", `consume(work.RequireTracked(id)); ` + mutate, true},
		{"goto bypass", `goto write; ` + track + `write: ` + mutate, true},
		{"wrong transaction", track + `return other.Q.ClearPageTags(ctx, id)`, true},
		{"replacement transaction", track + `work,err:=pagewrite.NewTx(q,principal); if err!=nil{return err}; ` + mutate, true},
		{"joined query handle", `work,err:=pagewrite.NewTx(q,principal); if err!=nil{return err}; ` + track + `return q.ClearPageTags(ctx,id)`, false},
		{"query alias", track + `q := work.Q; return q.ClearPageTags(ctx, id)`, false},
		{"query alias wrong transaction", track + `q := other.Q; return q.ClearPageTags(ctx, id)`, true},
		{"transaction alias", `tx := work; if _, err := tx.TrackSystem(ctx, id); err != nil { return err }; ` + mutate, false},
		{"ID alias", `savedID := id; ` + track + `return work.Q.ClearPageTags(ctx, savedID)`, false},
		{"ID reassigned", track + `id = otherID; ` + mutate, true},
		{"ID incremented", track + `id++; ` + mutate, true},
		{"ID compound assignment", track + `id+=otherID; ` + mutate, true},
		{"ID conditionally reassigned", track + `if flag { id = otherID }; ` + mutate, true},
		{"conditional tracking", `if flag { ` + track + `}; ` + mutate, true},
		{"loop tracking", `for flag { ` + track + `}; ` + mutate, true},
		{"unused closure", `_ = func() error { ` + track + `return nil }; ` + mutate, true},
		{"captured proof", track + `return run(func() error { ` + mutate + ` })`, false},
		{"new transaction closure", track + `return run(func(work *pagewrite.Tx) error { ` + mutate + ` })`, true},
		{"ignored tracking error", `work.TrackSystem(ctx,id); ` + mutate, true},
		{"unhandled tracking error", `_, err := work.TrackSystem(ctx,id); _ = err; ` + mutate, true},
		{"nonreturning error check", `_, err := work.TrackSystem(ctx,id); if err != nil { log(err) }; ` + mutate, true},
		{"required helper context", `if err := work.RequireTracked(id); err != nil { return err }; ` + mutate, false},
		{"wrong helper context", `if err := work.RequireTracked(otherID); err != nil { return err }; ` + mutate, true},
		{"read result ID", `p, err := work.Q.GetPageByID(ctx,id); if err != nil { return err }; ` + track + `params := pagewrite.ParamsFor(p); _, err = work.Q.UpdatePage(ctx,params); return err`, false},
		{"parameter ID", track + `params := store.UpdatePageParams{ID:id}; _, err := work.Q.UpdatePage(ctx,params); return err`, false},
		{"wrong parameter ID", track + `params := store.UpdatePageParams{ID:otherID}; _, err := work.Q.UpdatePage(ctx,params); return err`, true},
		{"changed parameter ID", track + `params := store.UpdatePageParams{ID:id}; params.ID=otherID; _, err := work.Q.UpdatePage(ctx,params); return err`, true},
		{"replaced page value", `p,err:=work.Q.GetPageByID(ctx,id); if err!=nil{return err}; ` + track + `p=loadOther(); return work.Q.ClearPageTags(ctx,p.ID)`, true},
		{"branch changes parameter ID", track + `params := store.UpdatePageParams{ID:id}; if flag { params.ID=otherID }; _, err := work.Q.UpdatePage(ctx,params); return err`, true},
		{"insert registered", `p, err := work.Q.CreatePage(ctx,params); if err != nil { return err }; work.Created(p); return work.Q.ClearPageTags(ctx,p.ID)`, false},
		{"existing page marked created", `p,err:=work.Q.GetPageByID(ctx,id); if err!=nil{return err}; work.Created(p); return work.Q.ClearPageTags(ctx,p.ID)`, true},
		{"insert unregistered", `p, err := work.Q.CreatePage(ctx,params); if err != nil { return err }; _=p; return nil`, true},
		{"insert returned directly", `return work.Q.CreatePage(ctx,params)`, true},
		{"insert discarded", `work.Q.CreatePage(ctx,params); return nil`, true},
		{"insert premature success", `p, err := work.Q.CreatePage(ctx,params); if err != nil { return nil }; work.Created(p); return nil`, true},
		{"insert registered on wrong transaction", `p, err := work.Q.CreatePage(ctx,params); if err != nil { return err }; other.Created(p); return nil`, true},
		{"insert conditional registration", `p, err := work.Q.CreatePage(ctx,params); if err != nil { return err }; if flag { work.Created(p) }; return nil`, true},
		{"insert late registration", `p, err := work.Q.CreatePage(ctx,params); if err != nil { return err }; err=work.Q.ClearPageTags(ctx,p.ID); work.Created(p); return err`, true},
		{"insert callback", `_,err := pagewrite.Insert(ctx,q,principal,params,func(work *pagewrite.Tx,p store.Page) error { return work.Q.ClearPageTags(ctx,p.ID) }); return err`, false},
		{"insert callback other page", `_,err := pagewrite.Insert(ctx,q,principal,params,func(work *pagewrite.Tx,p store.Page) error { return work.Q.ClearPageTags(ctx,otherID) }); return err`, true},
		{"lookalike callback", `_,err := other.Insert(ctx,q,principal,params,func(work *pagewrite.Tx,p store.Page) error { return work.Q.ClearPageTags(ctx,p.ID) }); return err`, true},
		{"translation both ends", `if err:=work.RequireTrackedEntity(kind,id,otherID); err!=nil {return err}; _,err:=work.Q.CreateTranslation(ctx,store.CreateTranslationParams{EntityType:kind,EntityID:id,TranslationID:otherID}); return err`, false},
		{"translation missing end", `if err:=work.RequireTrackedEntity(kind,id); err!=nil {return err}; _,err:=work.Q.CreateTranslation(ctx,store.CreateTranslationParams{EntityType:kind,EntityID:id,TranslationID:otherID}); return err`, true},
		{"media references", `if err:=work.TrackMedia(ctx,id); err!=nil {return err}; return work.Q.DeleteMedia(ctx,id)`, false},
		{"wrong media references", `if err:=work.TrackMedia(ctx,otherID); err!=nil {return err}; return work.Q.DeleteMedia(ctx,id)`, true},
		{"term cascade tracked", terms + trackTerms + `return work.Q.DeleteTag(ctx,id)`, false},
		{"term cascade missing tracking", terms + `return work.Q.DeleteTag(ctx,id)`, true},
		{"term cascade wrong term", terms + trackTerms + `return work.Q.DeleteTag(ctx,otherID)`, true},
		{"term cascade conditional tracking", terms + `if flag { ` + trackTerms + ` }; return work.Q.DeleteTag(ctx,id)`, true},
		{"term cascade late tracking", terms + `err=work.Q.DeleteTag(ctx,id); ` + trackTerms + `return err`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, "fixture.go", `package fixture; func f(ctx context.Context, work, other *pagewrite.Tx, id, otherID int64, flag bool) error { `+tc.body+` }`, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := trackedWriterDiagnostics(set, file, file.Decls[0].(*ast.FuncDecl))
			if (len(got) > 0) != tc.wantFailure {
				t.Fatalf("diagnostics = %v, want failure = %v", got, tc.wantFailure)
			}
		})
	}
}
