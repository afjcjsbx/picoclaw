//go:build custom_channels && channel_mail

package builtin_test

import "testing"

func TestMailFactoryRegisteredWhenSelected(t *testing.T) {
	assertMailFactoryRegistration(t, true)
}
