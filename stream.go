package http2

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type StreamState int32

const (
	StateIdle StreamState = iota
	StateReservedLocal
	StateReservedRemote
	StateOpen
	StateHalfClosedLocal
	StateHalfClosedRemote
	StateClosed
)

const defaultWeight = 16

type stream struct {
	conn *Conn

	id    uint32
	state StreamState

	weight   uint8
	parent   *stream
	children map[uint32]*stream

	recvWindow,
	recvWindowLowerBound,
	recvWindowUpperBound,
	processedBytes int

	sync.Mutex
	sendWindow,
	allocated,
	pendingBytes int
	pending   *list.List
	cancelled bool

	resetSent,
	resetReceived bool

	closed time.Time
}

var ignoreFrame = errors.New("ignored")

func (s *stream) transition(dir dir, frameType FrameType, endStream bool) (StreamState, error) {
	for {
		from := StreamState(atomic.LoadInt32((*int32)(&s.state)))
		to, ok := dir.transition(from, frameType, endStream)

		if !ok {
			if dir == send {
				if from == StateClosed {
					if frameType == FrameRSTStream {
						return from, ignoreFrame
					}
					// An endpoint MUST NOT send frames other than PRIORITY on a closed stream.
					return from, fmt.Errorf("stream %d already closed", s.id)
				}
				return from, fmt.Errorf("bad stream state %s", s.state)
			}

			// An endpoint that receives any frame other than PRIORITY
			// after receiving a RST_STREAM MUST treat that as a stream error
			// (Section 5.4.2) of type STREAM_CLOSED.
			if s.resetReceived {
				return from, StreamError{fmt.Errorf("stream %d already closed", s.id), ErrCodeStreamClosed, s.id}
			}

			if s.resetSent {
				// An endpoint MUST ignore frames that it
				// receives on closed streams after it has sent a RST_STREAM frame.
				// An endpoint MAY choose to limit the period over which it ignores
				// frames and treat frames that arrive after this time as being in error.
				if time.Since(s.closed) <= time.Duration(5)*time.Second {
					return from, ignoreFrame
				}
				return from, StreamError{fmt.Errorf("stream %d already closed", s.id), ErrCodeStreamClosed, s.id}
			}

			switch from {
			case StateHalfClosedRemote:
				// An endpoint that receives any frames after receiving a frame with the
				// END_STREAM flag set MUST treat that as a connection error
				// (Section 5.4.1) of type STREAM_CLOSED.
				return from, ConnError{fmt.Errorf("stream %d already closed", s.id), ErrCodeStreamClosed}
			case StateClosed:
				// WINDOW_UPDATE or RST_STREAM frames can be received in this state
				// for a short period after a DATA or HEADERS frame containing an
				// END_STREAM flag is sent.  Until the remote peer receives and
				// processes RST_STREAM or the frame bearing the END_STREAM flag, it
				// might send frames of these types.  Endpoints MUST ignore
				// WINDOW_UPDATE or RST_STREAM frames received in this state, though
				// endpoints MAY choose to treat frames that arrive a significant
				// time after sending END_STREAM as a connection error
				// (Section 5.4.1) of type PROTOCOL_ERROR.
				switch frameType {
				case FrameRSTStream, FrameWindowUpdate:
					if time.Since(s.closed) <= time.Duration(5)*time.Second {
						return from, ignoreFrame
					}
					return from, ConnError{fmt.Errorf("stream %d already closed", s.id), ErrCodeProtocol}
				}
			}
			return from, ConnError{fmt.Errorf("bad stream state %s", s.state), ErrCodeProtocol}
		}

		if s.compareAndSwapState(from, to) {
			if to == StateClosed && frameType == FrameRSTStream {
				if dir == recv {
					s.resetReceived = true
				} else {
					s.resetSent = true
				}
			}
			return to, nil
		}
	}
}

func (s *stream) setPriority(priority Priority) error {
	return nil
}

func (s *stream) active() bool {
	switch StreamState(atomic.LoadInt32((*int32)(&s.state))) {
	case StateOpen, StateHalfClosedLocal, StateHalfClosedRemote:
		return true
	default:
		return false
	}
}

func (s *stream) writable() bool {
	switch StreamState(atomic.LoadInt32((*int32)(&s.state))) {
	case StateOpen, StateHalfClosedRemote:
		return !s.cancelled
	default:
		return false
	}
}

func (s *stream) close() {
	for {
		from := StreamState(atomic.LoadInt32((*int32)(&s.state)))
		if s.compareAndSwapState(from, StateClosed) {
			return
		}
	}
}

func (s *stream) compareAndSwapState(from, to StreamState) bool {
	if atomic.CompareAndSwapInt32((*int32)(&s.state), int32(from), int32(to)) {
		switch to {
		case StateOpen, StateHalfClosedLocal, StateHalfClosedRemote:
			switch from {
			case StateIdle, StateReservedLocal, StateReservedRemote:
				if s.local() {
					atomic.AddUint32(&s.conn.numStreams, 1)
				} else {
					atomic.AddUint32(&s.conn.remote.numStreams, 1)
				}

				s.conn.setRecvWindow(s)

				if to != StateHalfClosedLocal {
					s.conn.setSendWindow(s)
				}
			case StateOpen:
				if to == StateHalfClosedLocal {
					s.conn.cancelStream(s)
				}
			}
		case StateClosed:
			switch from {
			case StateOpen, StateHalfClosedLocal, StateHalfClosedRemote:
				if s.local() {
					atomic.AddUint32(&s.conn.numStreams, ^uint32(0))
				} else {
					atomic.AddUint32(&s.conn.remote.numStreams, ^uint32(0))
				}
			}

			if from != StateClosed {
				s.closed = time.Now()
				s.conn.cancelStream(s)
				s.conn.removeStream(s)
			}
		}
		return true
	}
	return false
}

func (s *stream) local() bool {
	return localStreamID(s.conn.server, s.id)
}

func localStreamID(server bool, streamID uint32) bool {
	return server == ((streamID & 1) == 0)
}

type dir int

const (
	recv dir = iota
	send
)

func (dir dir) transition(from StreamState, frameType FrameType, endStream bool) (to StreamState, ok bool) {
	to = from

	if dir == recv {
		switch from {
		case StateIdle:
			switch frameType {
			case FrameHeaders:
				to = StateOpen
			case FramePriority:
			case FramePushPromise:
				to = StateReservedRemote
			default:
				return
			}
		case StateReservedLocal, StateHalfClosedRemote:
			switch frameType {
			case FramePriority, FrameWindowUpdate:
			case FrameRSTStream:
				to = StateClosed
			default:
				return
			}
		case StateReservedRemote:
			switch frameType {
			case FrameHeaders:
				to = StateHalfClosedLocal
			case FramePriority:
			case FrameRSTStream:
				to = StateClosed
			default:
				return
			}
		case StateOpen, StateHalfClosedLocal:
			switch frameType {
			case FrameRSTStream:
				to = StateClosed
			}
		case StateClosed:
			switch frameType {
			case FramePriority:
			default:
				return
			}
		}
	} else {
		switch from {
		case StateIdle:
			switch frameType {
			case FrameHeaders:
				to = StateOpen
			case FramePriority:
			case FramePushPromise:
				to = StateReservedLocal
			default:
				return
			}
		case StateReservedLocal:
			switch frameType {
			case FrameHeaders:
				to = StateHalfClosedRemote
			case FramePriority:
			case FrameRSTStream:
				to = StateClosed
			default:
				return
			}
		case StateReservedRemote, StateHalfClosedLocal:
			switch frameType {
			case FramePriority, FrameWindowUpdate:
			case FrameRSTStream:
				to = StateClosed
			default:
				return
			}
		case StateOpen:
			switch frameType {
			case FrameRSTStream:
				to = StateClosed
			}
		case StateHalfClosedRemote:
			switch frameType {
			case FrameData, FrameHeaders, FramePriority:
			case FrameRSTStream:
				to = StateClosed
			default:
				return
			}
		case StateClosed:
			switch frameType {
			case FramePriority:
			default:
				return
			}
		}
	}

	ok = true

	if endStream {
		switch to {
		case StateOpen:
			if dir == recv {
				to = StateHalfClosedRemote
			} else {
				to = StateHalfClosedLocal
			}
		case StateHalfClosedLocal, StateHalfClosedRemote:
			to = StateClosed
		}
	}

	return
}
