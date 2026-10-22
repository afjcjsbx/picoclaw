package bus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ErrBusClosed is returned when publishing to a closed MessageBus.
var ErrBusClosed = errors.New("message bus closed")

// ErrBusBackpressure is returned when a publish attempt exceeds the configured
// backpressure wait budget and the message is dropped.
var ErrBusBackpressure = errors.New("message bus backpressure")

var (
	ErrMissingInboundContext       = errors.New("inbound message context is required")
	ErrMissingOutboundContext      = errors.New("outbound message context is required")
	ErrMissingOutboundMediaContext = errors.New("outbound media context is required")
)

const defaultBusBufferSize = 64

// defaultAudioPublishTimeout is the default backpressure drop budget for audio
// chunks. It seeds the per-stream budget registry (see SetBackpressureTimeout)
// and can be overridden via SetBackpressureTimeout(StreamAudioChunk, d).
const defaultAudioPublishTimeout = 150 * time.Millisecond

// Stream identifiers used in publish policies, stats, per-stream backpressure
// configuration, and runtime events.
const (
	StreamInbound       = "inbound"
	StreamOutbound      = "outbound"
	StreamOutboundMedia = "outbound_media"
	StreamAudioChunk    = "audio_chunk"
	StreamVoiceControl  = "voice_control"
)

type publishPolicy struct {
	stream string
	// timeout is the per-stream backpressure drop budget resolved at publish
	// time from the budget registry (see SetBackpressureTimeout). When
	// positive, a full channel causes the message to be dropped after this
	// duration. When zero, the publish blocks until context cancellation or
	// bus close (no drop). Defaults: 0 for critical streams (inbound,
	// outbound, outbound_media, voice_control) and defaultAudioPublishTimeout
	// for audio_chunk; both are overridable per stream.
	timeout time.Duration
}

type streamStats struct {
	dropped       atomic.Uint64
	lastDropped   atomic.Int64
	lastWaitNanos atomic.Int64
}

type MessageBusStats struct {
	Inbound       StreamStats `json:"inbound"`
	Outbound      StreamStats `json:"outbound"`
	OutboundMedia StreamStats `json:"outbound_media"`
	AudioChunks   StreamStats `json:"audio_chunks"`
	VoiceControls StreamStats `json:"voice_controls"`
}

type StreamStats struct {
	Depth              int       `json:"depth"`
	Capacity           int       `json:"capacity"`
	DroppedTotal       uint64    `json:"dropped_total"`
	LastDroppedAt      time.Time `json:"last_dropped_at,omitempty"`
	LastDropWait       string    `json:"last_drop_wait,omitempty"`
	LastDropWaitMillis int64     `json:"last_drop_wait_ms,omitempty"`
}

// StreamDelegate is implemented by the channel Manager to provide streaming
// capabilities to the agent loop without tight coupling.
type StreamDelegate interface {
	// GetStreamer returns a Streamer for the given channel+chatID if the channel
	// supports streaming. Returns nil, false if streaming is unavailable.
	GetStreamer(ctx context.Context, channel, chatID, sessionKey string) (Streamer, bool)
}

// Streamer pushes incremental content to a streaming-capable channel.
// Defined here so the agent loop can use it without importing pkg/channels.
type Streamer interface {
	Update(ctx context.Context, content string) error
	Finalize(ctx context.Context, content string) error
	Cancel(ctx context.Context)
}

// ContextUsageStreamer can attach final context-window usage metadata when a
// streaming channel's final message replaces the normal outbound response.
type ContextUsageStreamer interface {
	Streamer
	FinalizeWithContext(ctx context.Context, content string, usage *ContextUsage) error
}

// ReasoningStreamer can show incremental model reasoning/thought content
// separately from the final user-visible answer stream.
type ReasoningStreamer interface {
	UpdateReasoning(ctx context.Context, content string) error
	FinalizeReasoning(ctx context.Context, content string) error
}

type MessageBus struct {
	inbound       chan InboundMessage
	outbound      chan OutboundMessage
	outboundMedia chan OutboundMediaMessage
	audioChunks   chan AudioChunk
	voiceControls chan VoiceControl

	closeOnce      sync.Once
	done           chan struct{}
	closed         atomic.Bool
	wg             sync.WaitGroup
	publishMu      sync.Mutex
	streamDelegate atomic.Value // stores StreamDelegate
	eventPublisher atomic.Value // stores EventPublisher
	backpressureMu sync.Mutex
	backpressure   atomic.Value // stores map[string]time.Duration
	inboundStats   streamStats
	outboundStats  streamStats
	mediaStats     streamStats
	audioStats     streamStats
	voiceStats     streamStats
}

// EventPublisher is the minimal runtime event publisher used by MessageBus.
type EventPublisher interface {
	Publish(ctx context.Context, evt runtimeevents.Event) runtimeevents.PublishResult
	PublishNonBlocking(evt runtimeevents.Event) runtimeevents.PublishResult
}

func NewMessageBus() *MessageBus {
	mb := &MessageBus{
		inbound:       make(chan InboundMessage, defaultBusBufferSize),
		outbound:      make(chan OutboundMessage, defaultBusBufferSize),
		outboundMedia: make(chan OutboundMediaMessage, defaultBusBufferSize),
		audioChunks:   make(chan AudioChunk, defaultBusBufferSize*4), // Audio chunks need more buffer.
		voiceControls: make(chan VoiceControl, defaultBusBufferSize),
		done:          make(chan struct{}),
	}
	mb.backpressure.Store(map[string]time.Duration{
		StreamAudioChunk: defaultAudioPublishTimeout,
	})
	return mb
}

func (mb *MessageBus) enterPublish(ctx context.Context) error {
	mb.publishMu.Lock()
	defer mb.publishMu.Unlock()

	if mb.closed.Load() {
		return ErrBusClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-mb.done:
		return ErrBusClosed
	default:
	}

	mb.wg.Add(1)
	return nil
}

func publish[T any](
	ctx context.Context,
	mb *MessageBus,
	ch chan T,
	msg T,
	policy publishPolicy,
	stats *streamStats,
	scope runtimeevents.Scope,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := mb.enterPublish(ctx); err != nil {
		return err
	}
	defer mb.wg.Done()

	// timeout == 0 means no backpressure drop budget; block until context
	// cancellation or bus close. This is the default for critical streams
	// (inbound, outbound, outbound_media, voice_control) where dropping
	// messages silently is undesirable. A positive budget (as configured for
	// audio chunks) drops the message once the queue stays full for the
	// budget. Override either behavior with SetBackpressureTimeout.
	if policy.timeout > 0 {
		timer := time.NewTimer(policy.timeout)
		defer timer.Stop()

		select {
		case ch <- msg:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			droppedTotal := stats.dropped.Add(1)
			now := time.Now()
			stats.lastDropped.Store(now.UnixNano())
			stats.lastWaitNanos.Store(policy.timeout.Nanoseconds())
			queueDepth := len(ch)
			queueCap := cap(ch)
			mb.publishDrop(
				policy.stream, scope, "queue_full_timeout",
				policy.timeout, queueDepth, queueCap, droppedTotal,
			)
			logger.WarnCF("bus", "Dropped bus message due to backpressure", map[string]any{
				"stream":         policy.stream,
				"wait_ms":        policy.timeout.Milliseconds(),
				"queue_depth":    queueDepth,
				"queue_capacity": queueCap,
				"dropped_total":  droppedTotal,
			})
			return fmt.Errorf("%w: %s queue full after %s", ErrBusBackpressure, policy.stream, policy.timeout)
		case <-mb.done:
			return ErrBusClosed
		}
	}

	select {
	case ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-mb.done:
		return ErrBusClosed
	}
}

func (mb *MessageBus) PublishInbound(ctx context.Context, msg InboundMessage) error {
	msg = NormalizeInboundMessage(msg)
	if msg.Context.isZero() {
		mb.publishFailure(StreamInbound, runtimeScopeFromInboundContext(msg.Context), ErrMissingInboundContext)
		return ErrMissingInboundContext
	}
	if err := publish(ctx, mb, mb.inbound, msg, publishPolicy{
		stream:  StreamInbound,
		timeout: mb.backpressureTimeout(StreamInbound),
	}, &mb.inboundStats, runtimeScopeFromInboundContext(msg.Context)); err != nil {
		scope := runtimeScopeFromInboundContext(msg.Context)
		if !errors.Is(err, ErrBusBackpressure) {
			mb.publishFailure(StreamInbound, scope, err)
		}
		return err
	}
	return nil
}

func (mb *MessageBus) InboundChan() <-chan InboundMessage {
	return mb.inbound
}

func (mb *MessageBus) PublishOutbound(ctx context.Context, msg OutboundMessage) error {
	msg = NormalizeOutboundMessage(msg)
	if msg.Context.isZero() {
		mb.publishFailure(StreamOutbound, runtimeScopeFromInboundContext(msg.Context), ErrMissingOutboundContext)
		return ErrMissingOutboundContext
	}
	if err := publish(ctx, mb, mb.outbound, msg, publishPolicy{
		stream:  StreamOutbound,
		timeout: mb.backpressureTimeout(StreamOutbound),
	}, &mb.outboundStats, runtimeScopeFromInboundContext(msg.Context)); err != nil {
		scope := runtimeScopeFromInboundContext(msg.Context)
		if !errors.Is(err, ErrBusBackpressure) {
			mb.publishFailure(StreamOutbound, scope, err)
		}
		return err
	}
	return nil
}

func (mb *MessageBus) OutboundChan() <-chan OutboundMessage {
	return mb.outbound
}

func (mb *MessageBus) PublishOutboundMedia(ctx context.Context, msg OutboundMediaMessage) error {
	msg = NormalizeOutboundMediaMessage(msg)
	if msg.Context.isZero() {
		mb.publishFailure(
			StreamOutboundMedia,
			runtimeScopeFromInboundContext(msg.Context),
			ErrMissingOutboundMediaContext,
		)
		return ErrMissingOutboundMediaContext
	}
	if err := publish(ctx, mb, mb.outboundMedia, msg, publishPolicy{
		stream:  StreamOutboundMedia,
		timeout: mb.backpressureTimeout(StreamOutboundMedia),
	}, &mb.mediaStats, runtimeScopeFromInboundContext(msg.Context)); err != nil {
		scope := runtimeScopeFromInboundContext(msg.Context)
		if !errors.Is(err, ErrBusBackpressure) {
			mb.publishFailure(StreamOutboundMedia, scope, err)
		}
		return err
	}
	return nil
}

func (mb *MessageBus) OutboundMediaChan() <-chan OutboundMediaMessage {
	return mb.outboundMedia
}

func (mb *MessageBus) PublishAudioChunk(ctx context.Context, chunk AudioChunk) error {
	if err := publish(ctx, mb, mb.audioChunks, chunk, publishPolicy{
		stream:  StreamAudioChunk,
		timeout: mb.backpressureTimeout(StreamAudioChunk),
	}, &mb.audioStats, runtimeScopeFromAudioChunk(chunk)); err != nil {
		scope := runtimeScopeFromAudioChunk(chunk)
		if !errors.Is(err, ErrBusBackpressure) {
			mb.publishFailure(StreamAudioChunk, scope, err)
		}
		return err
	}
	return nil
}

func (mb *MessageBus) AudioChunksChan() <-chan AudioChunk {
	return mb.audioChunks
}

func (mb *MessageBus) PublishVoiceControl(ctx context.Context, ctrl VoiceControl) error {
	if err := publish(ctx, mb, mb.voiceControls, ctrl, publishPolicy{
		stream:  StreamVoiceControl,
		timeout: mb.backpressureTimeout(StreamVoiceControl),
	}, &mb.voiceStats, runtimeScopeFromVoiceControl(ctrl)); err != nil {
		scope := runtimeScopeFromVoiceControl(ctrl)
		if !errors.Is(err, ErrBusBackpressure) {
			mb.publishFailure(StreamVoiceControl, scope, err)
		}
		return err
	}
	return nil
}

func (mb *MessageBus) VoiceControlsChan() <-chan VoiceControl {
	return mb.voiceControls
}

// SetStreamDelegate registers a StreamDelegate (typically the channel Manager).
func (mb *MessageBus) SetStreamDelegate(d StreamDelegate) {
	mb.streamDelegate.Store(d)
}

// SetEventPublisher registers a runtime event publisher for bus errors and lifecycle events.
func (mb *MessageBus) SetEventPublisher(p EventPublisher) {
	mb.eventPublisher.Store(p)
}

// SetBackpressureTimeout overrides the backpressure drop budget for a stream
// (see the StreamXxx constants). When timeout > 0, a publish that cannot
// enqueue within the budget while its queue is full is dropped with
// ErrBusBackpressure. When timeout <= 0 the publish blocks until context
// cancellation or bus close (no drop). Streams without an explicit override
// block (timeout 0).
func (mb *MessageBus) SetBackpressureTimeout(stream string, timeout time.Duration) {
	if timeout < 0 {
		timeout = 0
	}
	mb.backpressureMu.Lock()
	defer mb.backpressureMu.Unlock()

	current, _ := mb.backpressure.Load().(map[string]time.Duration)
	next := make(map[string]time.Duration, len(current)+1)
	for name, value := range current {
		next[name] = value
	}
	next[stream] = timeout
	mb.backpressure.Store(next)
}

// backpressureTimeout returns the configured drop budget for a stream, or 0
// (block until cancellation/close) when the stream has no override.
func (mb *MessageBus) backpressureTimeout(stream string) time.Duration {
	if timeouts, ok := mb.backpressure.Load().(map[string]time.Duration); ok {
		return timeouts[stream]
	}
	return 0
}

// GetStreamer returns a Streamer for the given channel+chatID+session via the delegate.
func (mb *MessageBus) GetStreamer(ctx context.Context, channel, chatID, sessionKey string) (Streamer, bool) {
	if d, ok := mb.streamDelegate.Load().(StreamDelegate); ok && d != nil {
		return d.GetStreamer(ctx, channel, chatID, sessionKey)
	}
	return nil, false
}

func (mb *MessageBus) Stats() MessageBusStats {
	if mb == nil {
		return MessageBusStats{}
	}
	return MessageBusStats{
		Inbound:       snapshotStreamStats(mb.inbound, &mb.inboundStats),
		Outbound:      snapshotStreamStats(mb.outbound, &mb.outboundStats),
		OutboundMedia: snapshotStreamStats(mb.outboundMedia, &mb.mediaStats),
		AudioChunks:   snapshotStreamStats(mb.audioChunks, &mb.audioStats),
		VoiceControls: snapshotStreamStats(mb.voiceControls, &mb.voiceStats),
	}
}

// HealthCheck returns a snapshot of queue depths and cumulative drop counts
// across all streams. It always reports ok=true: backpressure-induced drops are
// reflected in the message string for telemetry but do not affect the boolean.
// Callers that need readiness semantics (e.g. returning 503 when drops occur)
// should use HealthCheckWithThreshold.
func (mb *MessageBus) HealthCheck() (bool, string) {
	_, message := mb.healthSnapshot()
	return true, message
}

// HealthCheckWithThreshold behaves like HealthCheck but reports ok=false when
// the cumulative number of dropped messages across all streams exceeds
// maxDrops. Use maxDrops=0 to fail readiness on the first drop.
func (mb *MessageBus) HealthCheckWithThreshold(maxDrops uint64) (bool, string) {
	totalDropped, message := mb.healthSnapshot()
	return totalDropped <= maxDrops, message
}

func (mb *MessageBus) healthSnapshot() (uint64, string) {
	stats := mb.Stats()
	totalDropped := stats.Inbound.DroppedTotal +
		stats.Outbound.DroppedTotal +
		stats.OutboundMedia.DroppedTotal +
		stats.AudioChunks.DroppedTotal +
		stats.VoiceControls.DroppedTotal
	message := fmt.Sprintf(
		"in=%d/%d out=%d/%d media=%d/%d audio=%d/%d voice=%d/%d dropped=%d",
		stats.Inbound.Depth,
		stats.Inbound.Capacity,
		stats.Outbound.Depth,
		stats.Outbound.Capacity,
		stats.OutboundMedia.Depth,
		stats.OutboundMedia.Capacity,
		stats.AudioChunks.Depth,
		stats.AudioChunks.Capacity,
		stats.VoiceControls.Depth,
		stats.VoiceControls.Capacity,
		totalDropped,
	)

	return totalDropped, message
}

func snapshotStreamStats[T any](ch chan T, stats *streamStats) StreamStats {
	snapshot := StreamStats{
		DroppedTotal: stats.dropped.Load(),
	}
	if ch != nil {
		snapshot.Depth = len(ch)
		snapshot.Capacity = cap(ch)
	}
	if unixNano := stats.lastDropped.Load(); unixNano > 0 {
		snapshot.LastDroppedAt = time.Unix(0, unixNano)
	}
	if waitNanos := stats.lastWaitNanos.Load(); waitNanos > 0 {
		wait := time.Duration(waitNanos)
		snapshot.LastDropWait = wait.String()
		snapshot.LastDropWaitMillis = wait.Milliseconds()
	}
	return snapshot
}

func (mb *MessageBus) Close() {
	mb.closeOnce.Do(func() {
		mb.publishCloseEvent(runtimeevents.KindBusCloseStarted, 0)

		mb.publishMu.Lock()
		mb.closed.Store(true)
		close(mb.done)
		mb.publishMu.Unlock()

		// wait for all ongoing Publish calls to finish, ensuring all messages have been sent to channels or exited
		mb.wg.Wait()

		// close channels safely
		close(mb.inbound)
		close(mb.outbound)
		close(mb.outboundMedia)
		close(mb.audioChunks)
		close(mb.voiceControls)

		// clean up any remaining messages in channels
		drained := 0
		for range mb.inbound {
			drained++
		}
		for range mb.outbound {
			drained++
		}
		for range mb.outboundMedia {
			drained++
		}
		for range mb.audioChunks {
			drained++
		}
		for range mb.voiceControls {
			drained++
		}

		if drained > 0 {
			logger.DebugCF("bus", "Drained buffered messages during close", map[string]any{
				"count": drained,
			})
			mb.publishCloseEvent(runtimeevents.KindBusCloseDrained, drained)
		}
		mb.publishCloseEvent(runtimeevents.KindBusCloseCompleted, drained)
	})
}
