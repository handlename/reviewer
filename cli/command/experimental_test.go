package command

import (
	"io"
	"os"
	"testing"

	"github.com/alecthomas/kong"
)

// The Reply Notification is enabled only through REVIEWER_EXPERIMENTAL_REPLY_NOTIFICATION, for
// both commands that serve a review page. kong owns the parsing, so a typo fails at startup
// rather than quietly leaving the feature off.
func TestExperimentalReplyNotificationEnv(t *testing.T) {
	cases := []struct {
		value   string
		set     bool
		want    bool
		wantErr bool
	}{
		{set: false, want: false},
		{value: "", set: true, wantErr: true},
		{value: "0", set: true, want: false},
		{value: "false", set: true, want: false},
		{value: "1", set: true, want: true},
		{value: "true", set: true, want: true},
		{value: "yes please", set: true, wantErr: true},
	}
	commands := map[string]func(*Root) bool{
		"mcp":   func(r *Root) bool { return r.MCP.ExperimentalReplyNotification },
		"serve": func(r *Root) bool { return r.Serve.ExperimentalReplyNotification },
	}
	for cmd, field := range commands {
		for _, tc := range cases {
			// t.Setenv first so that the original value is restored even when the case unsets it.
			t.Setenv("REVIEWER_EXPERIMENTAL_REPLY_NOTIFICATION", tc.value)
			if !tc.set {
				os.Unsetenv("REVIEWER_EXPERIMENTAL_REPLY_NOTIFICATION")
			}

			var root Root
			parser, err := kong.New(&root,
				kong.Vars{"version": "reviewer vtest"},
				kong.Writers(io.Discard, io.Discard),
				kong.Exit(func(int) {}),
			)
			if err != nil {
				t.Fatalf("failed to build parser: %v", err)
			}
			args := []string{cmd}
			if cmd == "serve" {
				args = append(args, "experimental_test.go")
			}
			_, err = parser.Parse(args)
			if tc.wantErr {
				if err == nil {
					t.Errorf("%s, env=%q: want a parse error, got none", cmd, tc.value)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%s, env=%q: parse error = %v", cmd, tc.value, err)
			}
			if got := field(&root); got != tc.want {
				t.Errorf("%s, env=%q: ExperimentalReplyNotification = %t, want %t", cmd, tc.value, got, tc.want)
			}
		}
	}
}
