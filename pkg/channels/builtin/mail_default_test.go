//go:build !custom_channels || !channel_mail

package builtin_test

import "testing"

func TestMailFactoryNotRegisteredUnlessSelected(t *testing.T) {
	assertMailFactoryRegistration(t, false)
}
