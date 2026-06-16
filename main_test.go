package main

import (
	"reflect"
	"testing"
)

func TestEditorCommand(t *testing.T) {
	t.Run("default vi", func(t *testing.T) {
		t.Setenv("EDITOR", "")
		t.Setenv("VISUAL", "")
		if got := editorCommand(); !reflect.DeepEqual(got, []string{"vi"}) {
			t.Errorf("got %v, want [vi]", got)
		}
	})

	t.Run("EDITOR wins and splits", func(t *testing.T) {
		t.Setenv("EDITOR", "code -w")
		t.Setenv("VISUAL", "vim")
		if got := editorCommand(); !reflect.DeepEqual(got, []string{"code", "-w"}) {
			t.Errorf("got %v, want [code -w]", got)
		}
	})

	t.Run("VISUAL fallback when EDITOR blank", func(t *testing.T) {
		t.Setenv("EDITOR", "   ")
		t.Setenv("VISUAL", "emacs")
		if got := editorCommand(); !reflect.DeepEqual(got, []string{"emacs"}) {
			t.Errorf("got %v, want [emacs]", got)
		}
	})
}
