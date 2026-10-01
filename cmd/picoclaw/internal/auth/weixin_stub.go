//go:build custom_channels && !channel_weixin

package auth

import "github.com/spf13/cobra"

func registerWeixinCommand(*cobra.Command) {}
