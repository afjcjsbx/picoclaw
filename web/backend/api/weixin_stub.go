//go:build custom_channels && !channel_weixin

package api

import (
	"net/http"
	"time"
)

// weixinFlow is retained in Handler's state shape when the feature is omitted.
type weixinFlow struct {
	ID        string
	UpdatedAt time.Time
}

func (h *Handler) registerWeixinRoutes(*http.ServeMux) {}
