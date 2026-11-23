package hardwaretools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var spiDevicePattern = regexp.MustCompile(`^\d+\.\d+$`)

// WritePolicy is the shared read-only/allowlist/rate-limit gate for hardware
// tools. A nil policy is treated as read-only with no allowlist.
type WritePolicy struct {
	Tool         string
	AllowWrite   bool
	Allow        []string
	DryRun       bool
	MaxPerMinute int

	mu     sync.Mutex
	writes []time.Time
}

func NewWritePolicy(tool string, allowWrite bool, allow []string, dryRun bool, maxPerMinute int) *WritePolicy {
	return &WritePolicy{
		Tool:         tool,
		AllowWrite:   allowWrite,
		Allow:        append([]string(nil), allow...),
		DryRun:       dryRun,
		MaxPerMinute: maxPerMinute,
	}
}

func NewI2CWritePolicy(allowWrite bool, allow []string, dryRun bool, maxPerMinute int) *WritePolicy {
	normalized := make([]string, 0, len(allow))
	for _, entry := range allow {
		if device, ok := normalizeI2CAllowEntry(entry); ok {
			normalized = append(normalized, device)
		}
	}
	return NewWritePolicy("i2c", allowWrite, normalized, dryRun, maxPerMinute)
}

func NewSPIWritePolicy(allowWrite bool, allow []string, dryRun bool, maxPerMinute int) *WritePolicy {
	normalized := make([]string, 0, len(allow))
	for _, entry := range allow {
		device := strings.TrimSpace(entry)
		if spiDevicePattern.MatchString(device) {
			normalized = append(normalized, device)
		}
	}
	return NewWritePolicy("spi", allowWrite, normalized, dryRun, maxPerMinute)
}

func NewSerialWritePolicy(allowWrite bool, allow []string, dryRun bool, maxPerMinute int) *WritePolicy {
	normalized := make([]string, 0, len(allow))
	for _, entry := range allow {
		port, err := serialNormalizePort(entry)
		if err != nil {
			normalized = append(normalized, strings.TrimSpace(entry))
			continue
		}
		normalized = append(normalized, port)
	}
	return NewWritePolicy("serial", allowWrite, normalized, dryRun, maxPerMinute)
}

func (p *WritePolicy) allowHint() string {
	return fmt.Sprintf("tools.%s.allow_write", p.Tool)
}

func (p *WritePolicy) listHint() string {
	return fmt.Sprintf("tools.%s.allow", p.Tool)
}

func (p *WritePolicy) allowsTarget(target string) bool {
	if len(p.Allow) == 0 {
		return true
	}
	for _, allowed := range p.Allow {
		if allowed == target {
			return true
		}
	}
	return false
}

func (p *WritePolicy) checkTarget(target string) *ToolResult {
	if p == nil || p.allowsTarget(target) {
		return nil
	}
	return ErrorResult(fmt.Sprintf(
		"device %q is not in %s; configured devices: %s",
		target,
		p.listHint(),
		strings.Join(p.Allow, ", "),
	))
}

// checkBus restricts bus-level operations (I2C scan) to buses that appear in
// the allowlist. An empty allowlist leaves discovery unrestricted.
func (p *WritePolicy) checkBus(bus string, targetPrefix string) *ToolResult {
	if p == nil || len(p.Allow) == 0 {
		return nil
	}
	for _, allowed := range p.Allow {
		if strings.HasPrefix(allowed, targetPrefix) {
			return nil
		}
	}
	return ErrorResult(fmt.Sprintf(
		"bus %q is not in %s; configured devices: %s",
		bus,
		p.listHint(),
		strings.Join(p.Allow, ", "),
	))
}

func (p *WritePolicy) rateLimit(now time.Time) (bool, time.Duration) {
	if p == nil || p.MaxPerMinute < 0 {
		return true, 0
	}
	limit := p.MaxPerMinute
	if limit == 0 {
		limit = 30
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	kept := p.writes[:0]
	for _, at := range p.writes {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	p.writes = kept
	if len(p.writes) >= limit {
		retry := time.Until(p.writes[0].Add(time.Minute))
		if retry < 0 {
			retry = 0
		}
		return false, retry
	}
	p.writes = append(p.writes, now)
	return true, 0
}

// checkWrite validates the target, the write switch, and the rate limit. A nil
// result means the write may proceed.
func (p *WritePolicy) checkWrite(target string, now time.Time) *ToolResult {
	if p == nil {
		return ErrorResult("hardware writes are disabled")
	}
	if result := p.checkTarget(target); result != nil {
		return result
	}
	if !p.AllowWrite {
		return ErrorResult(fmt.Sprintf(
			"writes are disabled for the %s tool; set %s=true and add the device to %s",
			p.Tool, p.allowHint(), p.listHint(),
		))
	}
	ok, retry := p.rateLimit(now)
	if !ok {
		return ErrorResult(fmt.Sprintf(
			"write rate limit exceeded for %s (%d/min); retry in %s",
			p.Tool, p.effectiveLimit(), retry.Round(time.Second),
		))
	}
	return nil
}

func (p *WritePolicy) effectiveLimit() int {
	if p == nil || p.MaxPerMinute == 0 {
		return 30
	}
	return p.MaxPerMinute
}

func normalizeI2CAllowEntry(entry string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(entry), ":")
	if len(parts) != 2 {
		return "", false
	}
	bus := strings.TrimSpace(parts[0])
	address := strings.TrimSpace(parts[1])
	if !isValidBusID(bus) {
		return "", false
	}
	value, err := strconv.ParseUint(address, 0, 8)
	if err != nil || value < 0x03 || value > 0x77 {
		return "", false
	}
	return fmt.Sprintf("%s:0x%02x", bus, value), true
}

func i2cTarget(bus string, address int) string {
	return fmt.Sprintf("%s:0x%02x", bus, address)
}
