package netbind

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNormalizeHostInput(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "single host", raw: "127.0.0.1", want: "127.0.0.1"},
		{name: "trim and dedupe", raw: " [::1] , ::1 , 127.0.0.1 ", want: "::1,127.0.0.1"},
		{name: "star preserved", raw: "*,127.0.0.1", want: "*,127.0.0.1"},
		{name: "reject empty", raw: "127.0.0.1, ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeHostInput(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeHostInput() err = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want {
				t.Fatalf("NormalizeHostInput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildPlan_DefaultAnyUsesLoopbackProbe(t *testing.T) {
	plan, err := BuildPlan("", DefaultAny)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if plan.ProbeHost != ResolveAdaptiveLoopbackHost() {
		t.Fatalf("ProbeHost = %q, want %q", plan.ProbeHost, ResolveAdaptiveLoopbackHost())
	}
}

func TestOpenPlan_LocalhostSupportsLoopbackCommunication(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()

	plan, err := BuildPlan("localhost", DefaultLoopback)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	startTestHTTPServer(t, result.Listeners)
	port := mustAtoi(t, result.Port)

	if hasIPv6 {
		requireHTTPReachable(t, "::1", port)
	}
	if hasIPv4 {
		requireHTTPReachable(t, "127.0.0.1", port)
	}
}

func TestOpenPlan_DefaultAnySupportsDualStackLoopback(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()

	plan, err := BuildPlan("", DefaultAny)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	startTestHTTPServer(t, result.Listeners)
	port := mustAtoi(t, result.Port)

	if hasIPv6 {
		requireHTTPReachable(t, "::1", port)
	}
	if hasIPv4 {
		requireHTTPReachable(t, "127.0.0.1", port)
	}

	switch {
	case hasIPv4 && hasIPv6:
		if len(result.BindHosts) != 2 {
			t.Fatalf("len(BindHosts) = %d, want 2 (%#v)", len(result.BindHosts), result.BindHosts)
		}
	case hasIPv6 || hasIPv4:
		if len(result.BindHosts) != 1 {
			t.Fatalf("len(BindHosts) = %d, want 1 (%#v)", len(result.BindHosts), result.BindHosts)
		}
	}
}

func TestOpenPlan_ExplicitIPv6AnyIsIPv6Only(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv6 {
		t.Skip("IPv6 is unavailable in this environment")
	}

	plan, err := BuildPlan("::", DefaultLoopback)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	startTestHTTPServer(t, result.Listeners)
	port := mustAtoi(t, result.Port)

	requireHTTPReachable(t, "::1", port)
	if hasIPv4 {
		requireHTTPUnreachable(t, "127.0.0.1", port)
	}
}

func TestOpenPlan_ExplicitIPv4AnyIsIPv4Only(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv4 {
		t.Skip("IPv4 is unavailable in this environment")
	}

	plan, err := BuildPlan("0.0.0.0", DefaultLoopback)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	startTestHTTPServer(t, result.Listeners)
	port := mustAtoi(t, result.Port)

	requireHTTPReachable(t, "127.0.0.1", port)
	if hasIPv6 {
		requireHTTPUnreachable(t, "::1", port)
	}
}

func TestOpenPlan_MultiHostSupportsExplicitIPv4AndIPv6(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv4 || !hasIPv6 {
		t.Skip("dual-stack loopback is unavailable in this environment")
	}

	plan, err := BuildPlan("127.0.0.1,::1", DefaultLoopback)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	startTestHTTPServer(t, result.Listeners)
	port := mustAtoi(t, result.Port)

	requireHTTPReachable(t, "127.0.0.1", port)
	requireHTTPReachable(t, "::1", port)
}

func TestOpenPlan_WildcardRulesKeepIPv4AndIPv6AnyHosts(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv4 || !hasIPv6 {
		t.Skip("dual-stack loopback is unavailable in this environment")
	}

	plan, err := BuildPlan("::,::1,0.0.0.0,127.0.0.1", DefaultLoopback)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	startTestHTTPServer(t, result.Listeners)
	port := mustAtoi(t, result.Port)

	requireHTTPReachable(t, "127.0.0.1", port)
	requireHTTPReachable(t, "::1", port)
	if len(result.BindHosts) != 2 {
		t.Fatalf("len(BindHosts) = %d, want 2 (%#v)", len(result.BindHosts), result.BindHosts)
	}
}

func TestOpenPlan_DegradedWhenSiblingFamilyBindFails(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv4 || !hasIPv6 {
		t.Skip("dual-stack is unavailable in this environment")
	}

	// Occupy the IPv4 wildcard on a fixed port so the IPv4 half of the
	// dual-stack bind fails while IPv6 still succeeds. Before this was reported,
	// the IPv6 listener was kept and the lost IPv4 family went unnoticed.
	blocker, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer blocker.Close()
	port := strconv.Itoa(blocker.Addr().(*net.TCPAddr).Port)

	plan, err := BuildPlan("", DefaultAny)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, port)
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	t.Cleanup(func() {
		for _, ln := range result.Listeners {
			_ = ln.Close()
		}
	})

	if len(result.Listeners) != 1 {
		t.Fatalf("len(Listeners) = %d, want 1 (IPv6 should keep serving)", len(result.Listeners))
	}
	if len(result.Degraded) != 1 {
		t.Fatalf("len(Degraded) = %d, want 1 (%#v)", len(result.Degraded), result.Degraded)
	}
	if !strings.Contains(result.Degraded[0], "0.0.0.0") {
		t.Fatalf("Degraded[0] = %q, want it to name the failed IPv4 bind", result.Degraded[0])
	}
}

func TestOpenPlan_ErrorsWhenNoFamilyBinds(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv4 || !hasIPv6 {
		t.Skip("dual-stack is unavailable in this environment")
	}

	blocker4, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer blocker4.Close()
	port := strconv.Itoa(blocker4.Addr().(*net.TCPAddr).Port)

	blocker6, _, err := openExactListener(
		exactBinding{host: "::", network: "tcp6", v6Only: true},
		port,
	)
	if err != nil {
		t.Fatalf("openExactListener() error = %v", err)
	}
	defer blocker6.Close()

	plan, err := BuildPlan("", DefaultAny)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	if _, err = OpenPlan(plan, port); err == nil {
		t.Fatal("OpenPlan() error = nil, want an error when no family can bind")
	}
}

func TestOpenPlan_HealthyDualStackHasNoDegradedEntries(t *testing.T) {
	hasIPv4, hasIPv6 := DetectIPFamilies()
	if !hasIPv4 || !hasIPv6 {
		t.Skip("dual-stack is unavailable in this environment")
	}

	plan, err := BuildPlan("", DefaultAny)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}
	result, err := OpenPlan(plan, "0")
	if err != nil {
		t.Fatalf("OpenPlan() error = %v", err)
	}
	t.Cleanup(func() {
		for _, ln := range result.Listeners {
			_ = ln.Close()
		}
	})

	if len(result.Degraded) != 0 {
		t.Fatalf("len(Degraded) = %d, want 0 (%#v)", len(result.Degraded), result.Degraded)
	}
}

func TestOpenBindings_UnavailableFamilyIsNotDegraded(t *testing.T) {
	// 192.0.2.1 (TEST-NET-1) is never assigned to a local interface, so binding
	// it fails the same way a missing IP family does (EADDRNOTAVAIL).
	unassigned := exactBinding{host: "192.0.2.1", network: "tcp4"}
	ln, _, probeErr := openExactListener(unassigned, "0")
	if probeErr == nil {
		_ = ln.Close()
		t.Skip("192.0.2.1 is bindable in this environment")
	}
	if !isFamilyUnavailable(probeErr) {
		t.Skipf("binding an unassigned address did not report an unavailable family: %v", probeErr)
	}

	listeners, hosts, _, degraded, err := openBindings(
		[]exactBinding{unassigned, {host: "127.0.0.1", network: "tcp4"}},
		"0",
	)
	if err != nil {
		t.Fatalf("openBindings() error = %v", err)
	}
	t.Cleanup(func() {
		for _, l := range listeners {
			_ = l.Close()
		}
	})

	if len(listeners) != 1 || len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("listeners = %d, hosts = %v, want only 127.0.0.1", len(listeners), hosts)
	}
	if len(degraded) != 0 {
		t.Fatalf("Degraded = %#v, want none for an unavailable IP family", degraded)
	}
}

func TestOpenBindings_ErrorListsUnavailableFamilyWhenNothingBinds(t *testing.T) {
	unassigned := exactBinding{host: "192.0.2.1", network: "tcp4"}
	ln, _, probeErr := openExactListener(unassigned, "0")
	if probeErr == nil {
		_ = ln.Close()
		t.Skip("192.0.2.1 is bindable in this environment")
	}
	if !isFamilyUnavailable(probeErr) {
		t.Skipf("binding an unassigned address did not report an unavailable family: %v", probeErr)
	}

	listeners, hosts, boundPort, degraded, err := openBindings([]exactBinding{unassigned}, "0")
	if err == nil {
		t.Fatal("openBindings() error = nil, want an error when no binding opens")
	}
	if listeners != nil || hosts != nil || boundPort != "" || degraded != nil {
		t.Fatalf("openBindings() = %v, %v, %q, %v, want zero values on error", listeners, hosts, boundPort, degraded)
	}
	if !strings.Contains(err.Error(), "192.0.2.1") {
		t.Fatalf("openBindings() error = %q, want it to name the failed bind", err)
	}
}

func startTestHTTPServer(t *testing.T, listeners []net.Listener) {
	t.Helper()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok")
		}),
	}

	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		ln := listener
		go func() {
			errCh <- server.Serve(ln)
		}()
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		for range listeners {
			err := <-errCh
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Fatalf("server.Serve() error = %v", err)
			}
		}
	})
}

func requireHTTPReachable(t *testing.T, host string, port int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		err := httpGET(host, port)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %s:%d to be reachable: %v", host, port, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func requireHTTPUnreachable(t *testing.T, host string, port int) {
	t.Helper()

	if err := httpGET(host, port); err == nil {
		t.Fatalf("expected %s:%d to be unreachable", host, port)
	}
}

func httpGET(host string, port int) error {
	client := &http.Client{
		Timeout: 300 * time.Millisecond,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}

	resp, err := client.Get("http://" + net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return nil
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("Atoi(%q) error = %v", value, err)
	}
	return n
}
