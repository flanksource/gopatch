package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/clicky/api/icons"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

type plannedChange struct {
	Role, Module, Action, Path string
}

func (change plannedChange) Pretty() api.Text {
	icon := icons.Edit
	style := "text-yellow-500"
	switch change.Action {
	case "create":
		icon, style = icons.Add, "text-green-500"
	case "delete":
		icon, style = icons.Delete, "text-red-500"
	}
	text := api.Text{}.Add(icon).Space().Append(change.Role, "font-bold")
	if change.Module != "" {
		text = text.Space().Append(change.Module, "text-muted")
	}
	return text.Space().Append(change.Action, style).Space().Append(change.Path, "text-blue-500")
}

func (cmd *mainCmd) printPlannedEdits(edits []typed.FileEdit, producerRoot string) error {
	for _, edit := range edits {
		root := edit.ModuleRoot
		if root == "" {
			var err error
			root, err = moveModuleRoot(filepath.Dir(edit.Path))
			if err != nil {
				return err
			}
		}
		role, module := "target", ""
		if canonicalMovePath(root) != canonicalMovePath(producerRoot) {
			role, module = "consumer", edit.ModulePath
		}
		action := "update"
		if edit.Create {
			action = "create"
		} else if edit.Delete {
			action = "delete"
		}
		path := edit.Path
		if rel, err := filepath.Rel(root, edit.Path); err == nil {
			path = rel
		}
		line := plannedChange{Role: role, Module: module, Action: action, Path: path}.Pretty()
		if shouldUseColors(cmd.Stderr) {
			_, err := fmt.Fprintln(cmd.Stderr, strings.ReplaceAll(line.ANSI(), "\u00a0", " "))
			if err != nil {
				return err
			}
		} else if _, err := fmt.Fprintln(cmd.Stderr, strings.ReplaceAll(line.String(), "\u00a0", " ")); err != nil {
			return err
		}
	}
	return nil
}
