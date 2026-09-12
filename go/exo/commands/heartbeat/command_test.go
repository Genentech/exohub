package heartbeat

import "testing"

func TestNewCommandIncludesSubcommands(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatalf("expected command")
	}
	var hasRecord bool
	var hasShow bool
	for _, sub := range cmd.Commands() {
		switch sub.Use {
		case "record":
			hasRecord = true
		case "show":
			hasShow = true
		}
	}
	if !hasRecord || !hasShow {
		t.Fatalf("missing subcommands: record=%v show=%v", hasRecord, hasShow)
	}
}
