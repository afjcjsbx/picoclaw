package builtin_test

import (
	"slices"
	"testing"

	"github.com/sipeed/picoclaw/pkg/channels"
	_ "github.com/sipeed/picoclaw/pkg/channels/builtin"
	"github.com/sipeed/picoclaw/pkg/config"
)

func assertMailFactoryRegistration(t *testing.T, want bool) {
	t.Helper()

	got := slices.Contains(channels.GetRegisteredFactoryNames(), config.ChannelMail)
	if got != want {
		t.Fatalf("mail factory registered = %v, want %v", got, want)
	}
}
