// Package filter implements the filter extension of mosn
package filter

import (
	"context"

	"mosn.io/api"
	"mosn.io/mosn/pkg/types"
)

// UndefinedFilterPhase undefined filter phase, used for senderFilter.
const UndefinedFilterPhase api.FilterPhase = 99999

// StreamFilterChainStatus determines the running status of filter chain.
type StreamFilterChainStatus int

const (
	// StreamFilterChainContinue continues running the filter chain.
	StreamFilterChainContinue StreamFilterChainStatus = 0
	// StreamFilterChainStop stops running the filter chain, next time should retry the current filter.
	StreamFilterChainStop StreamFilterChainStatus = 1
	// StreamFilterChainReset stops running the filter chain and reset index, next time should run the first filter.
	StreamFilterChainReset StreamFilterChainStatus = 2
)

// StreamFilterStatusHandler converts api.StreamFilterStatus to StreamFilterChainStatus.
type StreamFilterStatusHandler func(status api.StreamFilterStatus) StreamFilterChainStatus

// DefaultStreamFilterStatusHandler is the default implementation of StreamFilterStatusHandler.
func DefaultStreamFilterStatusHandler(status api.StreamFilterStatus) StreamFilterChainStatus {
	switch status {
	case api.StreamFilterContinue:
		return StreamFilterChainContinue
	case api.StreamFilterStop:
		return StreamFilterChainReset
	case api.StreamFiltertermination:
		return StreamFilterChainReset
	}

	return StreamFilterChainContinue
}

// StreamFilterManager manages the lifecycle of streamFilters.
type StreamFilterManager interface {
	// register StreamSenderFilter, StreamReceiverFilter and AccessLog.
	api.StreamFilterChainFactoryCallbacks

	// invoke the receiver filter chain.
	RunReceiverFilter(ctx context.Context, phase api.FilterPhase,
		headers types.HeaderMap, data types.IoBuffer, trailers types.HeaderMap,
		statusHandler StreamFilterStatusHandler) api.StreamFilterStatus

	// invoke the sender filter chain.
	RunSenderFilter(ctx context.Context, phase api.FilterPhase,
		headers types.HeaderMap, data types.IoBuffer, trailers types.HeaderMap,
		statusHandler StreamFilterStatusHandler) api.StreamFilterStatus

	// invoke all access log.
	Log(ctx context.Context, reqHeaders api.HeaderMap, respHeaders api.HeaderMap, requestInfo api.RequestInfo)

	// destroy the sender filter chain and receiver filter chain.
	OnDestroy()
}

// StreamReceiverFilterWithPhase combines the StreamReceiverFilter with its Phase.
type StreamReceiverFilterWithPhase interface {
	api.StreamReceiverFilter
	ValidatePhase(phase api.FilterPhase) bool
}

// StreamReceiverFilterWithPhaseImpl is the default implementation of StreamReceiverFilterWithPhase.
type StreamReceiverFilterWithPhaseImpl struct {
	api.StreamReceiverFilter
	phase api.FilterPhase
}

// NewStreamReceiverFilterWithPhaseImpl returns a StreamReceiverFilterWithPhaseImpl struct..
func NewStreamReceiverFilterWithPhaseImpl(
	f api.StreamReceiverFilter, p api.FilterPhase) *StreamReceiverFilterWithPhaseImpl {
	return &StreamReceiverFilterWithPhaseImpl{
		StreamReceiverFilter: f,
		phase:                p,
	}
}

// ValidatePhase checks the current phase.
func (s *StreamReceiverFilterWithPhaseImpl) ValidatePhase(phase api.FilterPhase) bool {
	return s.phase == phase
}

// StreamSenderFilterWithPhase combines the StreamSenderFilter which its Phase.
type StreamSenderFilterWithPhase interface {
	api.StreamSenderFilter
	ValidatePhase(phase api.FilterPhase) bool
}

// StreamSenderFilterWithPhaseImpl is default implementation of StreamSenderFilterWithPhase.
type StreamSenderFilterWithPhaseImpl struct {
	api.StreamSenderFilter
	phase api.FilterPhase
}

// NewStreamSenderFilterWithPhaseImpl returns a new StreamSenderFilterWithPhaseImpl.
func NewStreamSenderFilterWithPhaseImpl(f api.StreamSenderFilter, p api.FilterPhase) *StreamSenderFilterWithPhaseImpl {
	return &StreamSenderFilterWithPhaseImpl{
		StreamSenderFilter: f,
		phase:              p,
	}
}

// ValidatePhase checks the current phase.
func (s *StreamSenderFilterWithPhaseImpl) ValidatePhase(phase api.FilterPhase) bool {
	return true
}

// DefaultStreamFilterManagerImpl is default implementation of the StreamFilterManager.
type DefaultStreamFilterManagerImpl struct {
	senderFilters      []StreamSenderFilterWithPhase
	senderFiltersIndex int

	receiverFilters      []StreamReceiverFilterWithPhase
	receiverFiltersIndex int

	streamAccessLogs []api.AccessLog
}

// AddStreamSenderFilter registers senderFilters.
func (d *DefaultStreamFilterManagerImpl) AddStreamSenderFilter(filter api.StreamSenderFilter) {
	f := NewStreamSenderFilterWithPhaseImpl(filter, UndefinedFilterPhase)
	d.AddStreamSenderFilterWithPhase(f)
}

func (d *DefaultStreamFilterManagerImpl) AddStreamSenderFilterWithPhase(filter StreamSenderFilterWithPhase) {
	d.senderFilters = append(d.senderFilters, filter)
}

// AddStreamReceiverFilter registers receiver filters.
func (d *DefaultStreamFilterManagerImpl) AddStreamReceiverFilter(filter api.StreamReceiverFilter, p api.FilterPhase) {
	f := NewStreamReceiverFilterWithPhaseImpl(filter, p)
	d.AddStreamReceiverFilterWithPhase(f)
}

func (d *DefaultStreamFilterManagerImpl) AddStreamReceiverFilterWithPhase(filter StreamReceiverFilterWithPhase) {
	d.receiverFilters = append(d.receiverFilters, filter)
}

// AddStreamAccessLog registers access logger.
func (d *DefaultStreamFilterManagerImpl) AddStreamAccessLog(accessLog api.AccessLog) {
	d.streamAccessLogs = append(d.streamAccessLogs, accessLog)
}

// RunReceiverFilter invokes the receiver filter chain.
func (d *DefaultStreamFilterManagerImpl) RunReceiverFilter(ctx context.Context, phase api.FilterPhase,
	headers types.HeaderMap, data types.IoBuffer, trailers types.HeaderMap,
	statusHandler StreamFilterStatusHandler) (filterStatus api.StreamFilterStatus) {
	if statusHandler == nil {
		statusHandler = DefaultStreamFilterStatusHandler
	}

	filterStatus = api.StreamFilterContinue

	for ; d.receiverFiltersIndex < len(d.receiverFilters); d.receiverFiltersIndex++ {
		filter := d.receiverFilters[d.receiverFiltersIndex]
		if !filter.ValidatePhase(phase) {
			continue
		}

		filterStatus = filter.OnReceive(ctx, headers, data, trailers)

		chainStatus := statusHandler(filterStatus)
		switch chainStatus {
		case StreamFilterChainContinue:
			continue
		case StreamFilterChainStop:
			return
		case StreamFilterChainReset:
			d.receiverFiltersIndex = 0
			return
		default:
			continue
		}
	}

	d.receiverFiltersIndex = 0

	return
}

// RunSenderFilter invokes the sender filter chain.
func (d *DefaultStreamFilterManagerImpl) RunSenderFilter(ctx context.Context, phase api.FilterPhase,
	headers types.HeaderMap, data types.IoBuffer, trailers types.HeaderMap,
	statusHandler StreamFilterStatusHandler) (filterStatus api.StreamFilterStatus) {
	if statusHandler == nil {
		statusHandler = DefaultStreamFilterStatusHandler
	}

	filterStatus = api.StreamFilterContinue

	for ; d.senderFiltersIndex < len(d.senderFilters); d.senderFiltersIndex++ {
		filter := d.senderFilters[d.senderFiltersIndex]
		if !filter.ValidatePhase(phase) {
			continue
		}

		filterStatus = filter.Append(ctx, headers, data, trailers)

		chainStatus := statusHandler(filterStatus)
		switch chainStatus {
		case StreamFilterChainContinue:
			continue
		case StreamFilterChainStop:
			return
		case StreamFilterChainReset:
			d.receiverFiltersIndex = 0
			return
		default:
			continue
		}
	}

	d.senderFiltersIndex = 0

	return
}

// Log invokes all access loggers.
func (d *DefaultStreamFilterManagerImpl) Log(ctx context.Context,
	reqHeaders api.HeaderMap, respHeaders api.HeaderMap, requestInfo api.RequestInfo) {
	for _, l := range d.streamAccessLogs {
		l.Log(ctx, reqHeaders, respHeaders, requestInfo)
	}
}

// OnDestroy invokes the destroy callback of both sender filters and receiver filters.
func (d *DefaultStreamFilterManagerImpl) OnDestroy() {
	for _, filter := range d.receiverFilters {
		filter.OnDestroy()
	}

	for _, filter := range d.senderFilters {
		filter.OnDestroy()
	}
}
