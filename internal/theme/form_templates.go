// Copyright (c) 2025-2026 Oleg Ivanchenko
// SPDX-License-Identifier: GPL-3.0-or-later

package theme

import (
	"regexp"
	"text/template/parse"
)

var checkedAttribute = regexp.MustCompile(`(?i)^\s*checked(?:\s*=\s*(?:checked|"checked"|'checked'|""|''))?\s*$`)

// warnLegacyCheckboxMatching checks the form content and the templates it
// calls. Look at parsed conditions so comments and other uses of contains,
// such as a substring check on a title, don't trigger the diagnostic.
func (m *Manager) warnLegacyCheckboxMatching(theme *Theme) {
	seen := make(map[string]bool)
	var visitTemplate func(string)
	var visitList func(*parse.Tree, *parse.ListNode)
	visitList = func(tree *parse.Tree, list *parse.ListNode) {
		if list == nil {
			return
		}
		for _, node := range list.Nodes {
			var branch *parse.BranchNode
			switch n := node.(type) {
			case *parse.IfNode:
				if (emitsChecked(n.List) || emitsChecked(n.ElseList)) && callsContains(n.Pipe) {
					location, _ := tree.ErrorContext(n)
					m.logger.Warn("custom form uses contains to set checked",
						"theme", theme.Name, "template", location, "replacement", "checkedOption")
				}
				branch = &n.BranchNode
			case *parse.RangeNode:
				branch = &n.BranchNode
			case *parse.WithNode:
				branch = &n.BranchNode
			case *parse.TemplateNode:
				visitTemplate(n.Name)
			}
			if branch != nil {
				visitList(tree, branch.List)
				visitList(tree, branch.ElseList)
			}
		}
	}
	visitTemplate = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if tmpl := theme.Templates.Lookup(name); tmpl != nil && tmpl.Tree != nil {
			visitList(tmpl.Tree, tmpl.Tree.Root)
		}
	}
	visitTemplate("content_form")
}

func emitsChecked(list *parse.ListNode) bool {
	if list == nil || len(list.Nodes) != 1 {
		return false
	}
	literal, ok := list.Nodes[0].(*parse.TextNode)
	return ok && checkedAttribute.Match(literal.Text)
}

func callsContains(pipe *parse.PipeNode) bool {
	for _, command := range pipe.Cmds {
		if name, ok := command.Args[0].(*parse.IdentifierNode); ok && name.Ident == "contains" {
			return true
		}
		for _, arg := range command.Args {
			if nested, ok := arg.(*parse.PipeNode); ok && callsContains(nested) {
				return true
			}
		}
	}
	return false
}
