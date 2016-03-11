package http2

import (
	"errors"
	"sync"
)

// InitialRecvWindow returns the initial receive flow control
// window size for the given stream.
func (c *Conn) InitialRecvWindow(streamID uint32) uint32 {
	if streamID == 0 {
		return c.connStream.recvFlow.initialWindow()
	}
	if stream := c.stream(streamID); stream != nil {
		return stream.recvFlow.initialWindow()
	}
	return 0
}

// RecvWindow returns the portion of the recieve flow control
// window for the given stream that is currently available for
// receiving frames which are subject to flow control.
func (c *Conn) RecvWindow(streamID uint32) uint32 {
	var stream *stream
	if streamID == 0 {
		stream = c.connStream
	} else {
		stream = c.stream(streamID)
	}
	if stream != nil {
		if w := stream.recvFlow.window(); w > 0 {
			return uint32(w)
		}
	}
	return 0
}

func (c *Conn) setInitialRecvWindow(delta int) error {
	if delta == 0 {
		return nil
	}

	var errors StreamErrorList

	c.streamL.RLock()
	defer c.streamL.RUnlock()

	for _, stream := range c.streams {
		if stream.readable() {
			if err := stream.recvFlow.incrementInitialWindow(delta); err != nil {
				errors.add(stream.id, ErrCodeFlowControl, err)
			}
		}
	}

	return errors.Err()
}

type flowController struct {
	sync.RWMutex
	s *stream
	win,
	winLowerBound,
	winUpperBound,
	processedWin int
}

func (c *flowController) initialWindow() uint32 {
	c.RLock()
	win := c.winUpperBound
	c.RUnlock()

	return uint32(win)
}

func (c *flowController) incrementInitialWindow(delta int) error {
	if c.s.id == 0 {
		return nil
	}

	c.Lock()
	defer c.Unlock()

	err := c.updateWindow(delta)
	if err == nil {
		c.updateInitialWindow(delta)
	}
	return err
}

func (c *flowController) window() int {
	c.RLock()
	win := c.win
	c.RUnlock()

	return win
}

func (c *flowController) incrementWindow(delta int) error {
	c.Lock()
	defer c.Unlock()

	c.updateInitialWindow(delta)

	return c.windowUpdate()
}

func (c *flowController) consumedBytes() int {
	c.RLock()
	defer c.RUnlock()

	return c.processedWin - c.win
}

func (c *flowController) consumeBytes(n int) error {
	if n <= 0 {
		return nil
	}

	c.Lock()
	defer c.Unlock()

	if c.s.id != 0 {
		if err := c.s.conn.connStream.recvFlow.consumeBytes(n); err != nil {
			return err
		}
	}
	c.win -= n
	if c.win < c.winLowerBound {
		if c.s.id == 0 {
			return ConnError{errors.New("window size limit exceeded"), ErrCodeFlowControl}
		}
		return StreamError{errors.New("window size limit exceeded"), ErrCodeFlowControl, c.s.id}
	}
	return nil
}

func (c *flowController) returnBytes(delta int) error {
	c.Lock()
	defer c.Unlock()

	if c.s.id != 0 {
		if err := c.s.conn.connStream.recvFlow.returnBytes(delta); err != nil {
			return err
		}
	}
	if c.processedWin-delta < c.win {
		if c.s.id == 0 {
			return ConnError{errors.New("attempting to return too many bytes"), ErrCodeInternal}
		}
		return StreamError{errors.New("attempting to return too many bytes"), ErrCodeInternal, c.s.id}
	}
	c.processedWin -= delta
	return c.windowUpdate()
}

func (c *flowController) updateWindow(delta int) error {
	if delta > 0 && maxInitialWindowSize-delta < c.win {
		return errors.New("window size overflow")
	}
	c.win += delta
	c.processedWin += delta
	c.winLowerBound = 0
	if delta < 0 {
		c.winLowerBound = delta
	}
	return nil
}

func (c *flowController) updateInitialWindow(delta int) {
	n := c.winUpperBound + delta
	if n < defaultInitialWindowSize {
		n = defaultInitialWindowSize
	}
	if n > maxInitialWindowSize {
		n = maxInitialWindowSize
	}
	delta = n - c.winUpperBound
	c.winUpperBound += delta
}

func (c *flowController) windowUpdate() error {
	if c.winUpperBound <= 0 {
		return nil
	}

	const windowUpdateRatio = 0.5

	threshold := int(float32(c.winUpperBound) * windowUpdateRatio)
	if c.processedWin > threshold {
		return nil
	}

	delta := c.winUpperBound - c.processedWin
	if err := c.updateWindow(delta); err != nil {
		return ConnError{errors.New("attempting to return too many bytes"), ErrCodeInternal}
	}

	c.s.conn.writeQueue.add(&WindowUpdateFrame{c.s.id, uint32(delta)}, true)

	return nil
}

// InitialSendWindow returns the initial send flow control
// window size for the given stream.
func (c *Conn) InitialSendWindow(uint32) uint32 {
	return c.RemoteSettings().InitialWindowSize()
}

// SendWindow returns the portion of the send flow control
// window for the given stream that is currently available for
// sending frames which are subject to flow control.
func (c *Conn) SendWindow(streamID uint32) uint32 {
	var stream *stream
	if streamID == 0 {
		stream = c.connStream
	} else {
		stream = c.stream(streamID)
	}
	if stream != nil {
		if w := stream.sendFlow.window(); w > 0 {
			return uint32(w)
		}
	}
	return 0
}

func (c *Conn) setInitialSendWindow(delta int) error {
	if delta == 0 {
		return nil
	}

	var errors StreamErrorList

	c.streamL.RLock()
	defer c.streamL.RUnlock()

	for _, stream := range c.streams {
		if stream.writable() {
			if err := stream.sendFlow.incrementInitialWindow(delta); err != nil {
				if stream.id == 0 {
					return err
				}
				se := err.(StreamError)
				errors = append(errors, &se)
			}
		}
	}

	return errors.Err()
}

type remoteFlowController struct {
	sync.RWMutex
	s     *stream
	win   int
	winCh chan int
}

func (c *remoteFlowController) incrementInitialWindow(delta int) error {
	return c.updateWindow(delta, true)
}

func (c *remoteFlowController) window() int {
	c.RLock()
	win := c.win
	c.RUnlock()

	return win
}

func (c *remoteFlowController) incrementWindow(delta int) error {
	return c.updateWindow(delta, false)
}

func (c *remoteFlowController) updateWindow(delta int, reset bool) error {
	c.Lock()
	defer c.Unlock()

	if delta > 0 && maxInitialWindowSize-delta < c.win {
		if c.s.id == 0 {
			return ConnError{errors.New("window size overflow"), ErrCodeFlowControl}
		}
		return StreamError{errors.New("window size overflow"), ErrCodeFlowControl, c.s.id}
	}

	if reset {
		select {
		case n := <-c.winCh:
			c.win += n
		default:
		}
	}

	c.win += delta

	if c.win <= 0 {
		return nil
	}

	select {
	case c.winCh <- c.win:
		c.win = 0
	default:
	}

	return nil
}

func (c *remoteFlowController) windowCh() <-chan int {
	return c.winCh
}

func (c *remoteFlowController) cancel() {
	c.Lock()
	defer c.Unlock()

	select {
	case n := <-c.winCh:
		c.win += n
	default:
	}
}

func allocateBytes(stream *stream, n int) (int, error) {
	if n <= 0 {
		return 0, nil
	}

	c, s := stream.conn.connStream.sendFlow, stream.sendFlow

	s.incrementWindow(0)

	var sw int
	select {
	case <-stream.closeCh:
		return 0, errStreamClosed
	case <-stream.conn.closeCh:
		return 0, ErrClosed
	case sw = <-s.windowCh():
	}

	c.incrementWindow(0)

	var cw int
	select {
	case <-stream.closeCh:
		c.cancel()
		return 0, errStreamClosed
	case <-stream.conn.closeCh:
		return 0, ErrClosed
	case cw = <-c.windowCh():
	}

	if sw < n {
		n = sw
	}
	if cw < n {
		n = cw
	}

	if n < sw {
		s.incrementWindow(sw - n)
	}
	if n < cw {
		c.incrementWindow(cw - n)
	}

	return n, nil
}
