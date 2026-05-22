package main

import (
	"reflect"
	"testing"
)

func TestReorderSubcommand(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantCmd  string
		wantRest []string
		wantOK   bool
	}{
		{"opus before session", []string{"--opus", "glm-5v-turbo", "session"}, "session", []string{"--opus", "glm-5v-turbo"}, true},
		{"model before run", []string{"--model", "m", "run", "--dir", ".", "task"}, "run", []string{"--model", "m", "--dir", ".", "task"}, true},
		{"json before list", []string{"--json", "list"}, "list", []string{"--json"}, true},
		{"value equals a command name", []string{"--opus", "run", "session"}, "session", []string{"--opus", "run"}, true},
		{"short flag before session", []string{"-m", "x", "session"}, "session", []string{"-m", "x"}, true},
		{"no subcommand at all", []string{"--opus", "x"}, "", nil, false},
		{"unknown positional first", []string{"--opus", "x", "notacommand"}, "", nil, false},
	}
	for _, c := range cases {
		gotCmd, gotRest, gotOK := reorderSubcommand(c.args)
		if gotOK != c.wantOK || gotCmd != c.wantCmd || !reflect.DeepEqual(gotRest, c.wantRest) {
			t.Errorf("%s: reorderSubcommand(%v) = (%q, %v, %v), want (%q, %v, %v)",
				c.name, c.args, gotCmd, gotRest, gotOK, c.wantCmd, c.wantRest, c.wantOK)
		}
	}
}
