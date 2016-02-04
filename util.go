package http2

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
)

func (e ErrCode) String() string {
	switch e {
	case ErrCodeNo:
		return "NO_ERROR"
	case ErrCodeProtocol:
		return "PROTOCOL_ERROR"
	case ErrCodeInternal:
		return "INTERNAL_ERROR"
	case ErrCodeFlowControl:
		return "FLOW_CONTROL_ERROR"
	case ErrCodeSettingsTimeout:
		return "SETTINGS_TIMEOUT"
	case ErrCodeStreamClosed:
		return "STREAM_CLOSED"
	case ErrCodeFrameSize:
		return "FRAME_SIZE_ERROR"
	case ErrCodeRefusedStream:
		return "REFUSED_STREAM"
	case ErrCodeCancel:
		return "CANCEL"
	case ErrCodeCompression:
		return "COMPRESSION_ERROR"
	case ErrCodeConnect:
		return "CONNECT_ERROR"
	case ErrCodeEnhanceYourCalm:
		return "ENHANCE_YOUR_CALM"
	case ErrCodeInadequateSecurity:
		return "INADEQUATE_SECURITY"
	case ErrCodeHTTP11Required:
		return "HTTP_1_1_REQUIRED"
	default:
		return fmt.Sprintf("unknown error code 0x%x", uint32(e))
	}
}

func (e ConnError) Error() string {
	return fmt.Sprintf("connection error(%s): %s", e.ErrCode, e.Err.Error())
}

func (e StreamError) Error() string {
	return fmt.Sprintf("stream error(stream ID=%d; %s): %s", e.StreamID, e.ErrCode, e.Err.Error())
}

func (e *StreamErrorList) add(streamID uint32, errCode ErrCode, err error) {
	*e = append(*e, &StreamError{err, errCode, streamID})
}

func (e StreamErrorList) Error() string {
	switch len(e) {
	case 0:
		return "no errors"
	case 1:
		return e[0].Error()
	}
	return fmt.Sprintf("%s (and %d more stream errors)", e[0], len(e)-1)
}

func (e StreamErrorList) Err() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

func (e MalformedError) Error() string {
	return fmt.Sprintf("http2: malformed; %s", string(e))
}

func (id SettingID) String() string {
	switch id {
	case SettingHeaderTableSize:
		return "HEADER_TABLE_SIZE"
	case SettingEnablePush:
		return "ENABLE_PUSH"
	case SettingMaxConcurrentStreams:
		return "MAX_CONCURRENT_STREAMS"
	case SettingInitialWindowSize:
		return "INITIAL_WINDOW_SIZE"
	case SettingMaxFrameSize:
		return "MAX_FRAME_SIZE"
	case SettingMaxHeaderListSize:
		return "MAX_HEADER_LIST_SIZE"
	default:
		return fmt.Sprintf("UNKNOWN_SETTING_%d", uint16(id))
	}
}

func (s Settings) HeaderTableSize() uint32 {
	return s.Value(SettingHeaderTableSize)
}

func (s *Settings) SetHeaderTableSize(value uint32) error {
	return s.SetValue(SettingHeaderTableSize, value)
}

func (s Settings) PushEnabled() bool {
	return s.Value(SettingEnablePush) != 0
}

func (s *Settings) SetPushEnabled(enabled bool) error {
	var value uint32
	if enabled {
		value = 1
	}
	return s.SetValue(SettingEnablePush, value)
}

func (s Settings) MaxConcurrentStreams() uint32 {
	return s.Value(SettingMaxConcurrentStreams)
}

func (s *Settings) SetMaxConcurrentStreams(value uint32) error {
	return s.SetValue(SettingMaxConcurrentStreams, value)
}

func (s Settings) InitialWindowSize() uint32 {
	return s.Value(SettingInitialWindowSize)
}

func (s *Settings) SetInitialWindowSize(value uint32) error {
	return s.SetValue(SettingInitialWindowSize, value)
}

func (s Settings) MaxFrameSize() uint32 {
	return s.Value(SettingMaxFrameSize)
}

func (s *Settings) SetMaxFrameSize(value uint32) error {
	return s.SetValue(SettingMaxFrameSize, value)
}

func (s Settings) MaxHeaderListSize() uint32 {
	return s.Value(SettingMaxHeaderListSize)
}

func (s *Settings) SetMaxHeaderListSize(value uint32) error {
	return s.SetValue(SettingMaxHeaderListSize, value)
}

func (s Settings) Value(id SettingID) uint32 {
	if v, exists := s.value(id); exists {
		return v
	}
	switch id {
	case SettingHeaderTableSize:
		return defaultHeaderTableSize
	case SettingEnablePush:
		return defaultEnablePush
	case SettingMaxConcurrentStreams:
		return defaultMaxConcurrentStreams
	case SettingInitialWindowSize:
		return defaultInitialWindowSize
	case SettingMaxFrameSize:
		return defaultMaxFrameSize
	case SettingMaxHeaderListSize:
		return 0
	default:
		return 0
	}
}

func (s *Settings) SetValue(id SettingID, value uint32) error {
	ok := true
	switch id {
	case SettingEnablePush:
		ok = value < 2
	case SettingInitialWindowSize:
		ok = value <= maxInitialWindowSize
	case SettingMaxFrameSize:
		ok = maxFrameSizeLowerBound <= value && value <= maxFrameSizeUpperBound
	}
	if !ok {
		return fmt.Errorf("invalid %s specified; %v", id, value)
	}
	if *s == nil {
		const numStandardSettings = 6
		*s = make(Settings, numStandardSettings)
		*s = (*s)[:0]
	}
	ok = false
	for i := 0; i < len(*s); i++ {
		if (*s)[i].ID == id {
			(*s)[i].Value = value
			ok = true
			break
		}
	}
	if !ok {
		*s = append(*s, setting{id, value})
	}
	return nil
}

func (s Settings) value(id SettingID) (uint32, bool) {
	for _, x := range s {
		if x.ID == id {
			return x.Value, true
		}
	}
	return 0, false
}

func (s Settings) String() string {
	buf := bytes.NewBufferString("settings={")
	for i := 0; i < len(s); i++ {
		if i == 0 {
			fmt.Fprintf(buf, "%s:%d", s[i].ID, s[i].Value)
		} else {
			fmt.Fprintf(buf, ",%s:%d", s[i].ID, s[i].Value)
		}
	}
	buf.WriteString("}")
	return buf.String()
}

func (t FrameType) String() string {
	switch t {
	case FrameData:
		return "DATA"
	case FrameHeaders:
		return "HEADERS"
	case FramePriority:
		return "PRIORITY"
	case FrameRSTStream:
		return "RST_STREAM"
	case FrameSettings:
		return "SETTINGS"
	case FramePushPromise:
		return "PUSH_PROMISE"
	case FramePing:
		return "PING"
	case FrameGoAway:
		return "GOAWAY"
	case FrameWindowUpdate:
		return "WINDOW_UPDATE"
	case FrameContinuation:
		return "CONTINUATION"
	default:
		return fmt.Sprintf("UNKNOWN_FRAME_TYPE_%d", uint8(t))
	}
}

func (f *DataFrame) Type() FrameType         { return FrameData }
func (f *HeadersFrame) Type() FrameType      { return FrameHeaders }
func (f *PriorityFrame) Type() FrameType     { return FramePriority }
func (f *RSTStreamFrame) Type() FrameType    { return FrameRSTStream }
func (f *SettingsFrame) Type() FrameType     { return FrameSettings }
func (f *PushPromiseFrame) Type() FrameType  { return FramePushPromise }
func (f *PingFrame) Type() FrameType         { return FramePing }
func (f *GoAwayFrame) Type() FrameType       { return FrameGoAway }
func (f *WindowUpdateFrame) Type() FrameType { return FrameWindowUpdate }
func (f *UnknownFrame) Type() FrameType      { return f.FrameType }

func (f *DataFrame) Stream() uint32         { return f.StreamID }
func (f *HeadersFrame) Stream() uint32      { return f.StreamID }
func (f *PriorityFrame) Stream() uint32     { return f.StreamID }
func (f *RSTStreamFrame) Stream() uint32    { return f.StreamID }
func (f *SettingsFrame) Stream() uint32     { return 0 }
func (f *PushPromiseFrame) Stream() uint32  { return f.StreamID }
func (f *PingFrame) Stream() uint32         { return 0 }
func (f *GoAwayFrame) Stream() uint32       { return 0 }
func (f *WindowUpdateFrame) Stream() uint32 { return f.StreamID }
func (f *UnknownFrame) Stream() uint32      { return f.StreamID }

func (f *DataFrame) EndOfStream() bool         { return f.EndStream }
func (f *HeadersFrame) EndOfStream() bool      { return f.EndStream }
func (f *PriorityFrame) EndOfStream() bool     { return false }
func (f *RSTStreamFrame) EndOfStream() bool    { return false }
func (f *SettingsFrame) EndOfStream() bool     { return false }
func (f *PushPromiseFrame) EndOfStream() bool  { return false }
func (f *PingFrame) EndOfStream() bool         { return false }
func (f *GoAwayFrame) EndOfStream() bool       { return false }
func (f *WindowUpdateFrame) EndOfStream() bool { return false }
func (f *UnknownFrame) EndOfStream() bool      { return f.Flags.Has(FlagEndStream) }

func (f *HeadersFrame) HasPriority() bool { return f.Priority != Priority{} }

func (f Flags) Has(v Flags) bool { return (f & v) == v }

func (state StreamState) String() string {
	switch state {
	case StateIdle:
		return "Idle"
	case StateReservedLocal:
		return "ReservedLocal"
	case StateReservedRemote:
		return "ReservedRemote"
	case StateOpen:
		return "Open"
	case StateHalfClosedLocal:
		return "HalfClosedLocal"
	case StateHalfClosedRemote:
		return "HalfClosedRemote"
	case StateClosed:
		return "Closed"
	default:
		panic("bad stream state")
	}
}

func (h Header) Method() string {
	return h.get(":method")
}

func (h Header) SetMethod(value string) {
	h[":method"] = []string{value}
}

func (h Header) Scheme() string {
	return h.get(":scheme")
}

func (h Header) SetScheme(value string) {
	h[":scheme"] = []string{value}
}

func (h Header) Authority() string {
	return h.get(":authority")
}

func (h Header) SetAuthority(value string) {
	h[":authority"] = []string{value}
}

func (h Header) Path() string {
	return h.get(":path")
}

func (h Header) SetPath(value string) {
	h[":path"] = []string{value}
}

func (h Header) Status() string {
	return h.get(":status")
}

func (h Header) SetStatus(value string) {
	h[":status"] = []string{value}
}

func (h Header) Add(key, value string) {
	if key[0] != ':' {
		key = CanonicalHTTP2HeaderKey(key)
		h[key] = append(h[key], value)
	}
}

func (h Header) Set(key, value string) {
	h[CanonicalHTTP2HeaderKey(key)] = []string{value}
}

func (h Header) Get(key string) string {
	if h == nil {
		return ""
	}
	v := h[CanonicalHTTP2HeaderKey(key)]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func (h Header) Del(key string) {
	delete(h, CanonicalHTTP2HeaderKey(key))
}

func (h Header) Len() (n int) {
	if h == nil {
		return
	}
	for _, vv := range h {
		n += len(vv)
	}
	return
}

func (h Header) get(key string) string {
	if v := h[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

var errMalformedHeader = MalformedError("invalid header field")

func (h *Header) add(key, value string, _ bool) error {
	if key[0] == ':' {
		if h.Len() > 5 {
			return errMalformedHeader
		}
		if _, pseudo := pseudoHeader[key]; !pseudo {
			return errMalformedHeader
		}
	}
	if !validHeaderKey(key) {
		return errMalformedHeader
	}

	if *h == nil {
		*h = make(Header)
	}
	(*h)[key] = append((*h)[key], value)

	return nil
}

func (h *Header) addHeader(header map[string][]string) error {
	for k, vv := range header {
		k = CanonicalHTTP2HeaderKey(k)

		if badHeader(k) {
			continue
		}

		if k == "cookie" {
			for _, v := range vv {
				if i := strings.IndexByte(v, ';'); i > 0 {
					for _, c := range strings.Split(v, ";") {
						h.Add(k, strings.TrimSpace(c))
					}
				} else {
					h.Add(k, v)
				}
			}
			continue
		}

		if k == "te" && len(vv) != 1 && strings.ToLower(vv[0]) != "trailers" {
			return MalformedError(fmt.Sprintf("bad value for te: %s", vv[0]))
		}

		for _, v := range vv {
			h.Add(k, v)
		}
	}
	return nil
}

func requestToHeader(req *http.Request, skipVerify bool) (Header, error) {
	h := make(Header, len(req.Header))

	// All HTTP/2 requests MUST include exactly one valid value for the
	// ":method", ":scheme", and ":path" pseudo-header fields, unless it is
	// a CONNECT request (Section 8.3).
	if req.Method != "CONNECT" {
		h.SetMethod(req.Method)

		if scheme := req.URL.Scheme; scheme != "" {
			h.SetScheme(scheme)
		} else {
			if i := strings.LastIndexByte(req.URL.Host, ':'); i >= 0 && i > strings.LastIndexByte(req.URL.Host, ']') {
				switch req.URL.Host[i:] {
				case ":http", ":80":
					scheme = "http"
				case ":https", ":443":
					scheme = "https"
				}
			}

			if scheme != "" {
				h.SetScheme(scheme)
			} else if !skipVerify {
				return nil, MalformedError(":scheme must be specified")
			}
		}

		if path := req.URL.RequestURI(); path != "" {
			if req.URL.Fragment != "" {
				path += "#" + req.URL.Fragment
			}
			h.SetPath(path)
		} else if !skipVerify {
			return nil, MalformedError(":path must be specified")
		} else {
			h.SetPath("/")
		}
	}

	h.SetAuthority(req.URL.Host)

	if err := h.addHeader(req.Header); err != nil {
		return nil, err
	}

	return h, nil
}

func CanonicalHTTP2HeaderKey(s string) string {
	if v, ok := commonHeader[s]; ok {
		return v
	}
	if validHeaderKey(s) {
		return s
	}
	return strings.ToLower(s)
}

func validHeaderKey(v string) bool {
	if len(v) == 0 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= 127 || ('A' <= c && c <= 'Z') {
			return false
		}
	}
	return true
}

func splitHeader(header map[string][]string, key string) (values []string) {
	for k, v := range header {
		if strings.EqualFold(key, k) {
			for _, vv := range v {
				for _, s := range strings.Split(vv, ",") {
					values = append(values, strings.TrimSpace(s))
				}
			}
			break
		}
	}
	return
}

func containsValue(header map[string][]string, key string, values ...string) bool {
	ss := splitHeader(header, key)
	if len(ss) == 0 {
		return false
	}
loop:
	for _, v := range values {
		for _, s := range ss {
			if strings.EqualFold(v, s) {
				continue loop
			}
		}
		return false
	}
	return true
}

func badHeader(key string) bool {
	switch key {
	case
		"connection",
		"keep-alive",
		"proxy-connection",
		"transfer-encoding",
		"host",
		"upgrade":

		return true
	default:
		return false
	}
}

func badCipher(cipher uint16) bool {
	switch cipher {
	case
		tls.TLS_RSA_WITH_RC4_128_SHA,
		tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
		tls.TLS_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_RC4_128_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_RC4_128_SHA,
		tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA:

		return true
	default:
		return false
	}
}

var (
	pseudoHeader = make(map[string]struct{})
	commonHeader = make(map[string]string)
)

func init() {
	for _, v := range []string{
		":method",
		":scheme",
		":authority",
		":path",
		":status",
	} {
		pseudoHeader[v] = struct{}{}
	}

	for _, v := range []string{
		"Accept",
		"Accept-Charset",
		"Accept-Encoding",
		"Accept-Language",
		"Accept-Ranges",
		"Age",
		"Allow",
		"ALPN",
		"Authentication-Info",
		"Authorization",
		"Cache-Control",
		"Connection",
		"Content-Disposition",
		"Content-Encoding",
		"Content-Language",
		"Content-Length",
		"Content-Location",
		"Content-Range",
		"Content-Type",
		"Cookie",
		"DASL",
		"DAV",
		"Date",
		"Depth",
		"Destination",
		"ETag",
		"Expect",
		"Expires",
		"Forwarded",
		"From",
		"Host",
		"HTTP2-Settings",
		"If",
		"If-Match",
		"If-Modified-Since",
		"If-None-Match",
		"If-Range",
		"If-Schedule-Tag-Match",
		"If-Unmodified-Since",
		"Last-Modified",
		"Location",
		"Lock-Token",
		"Max-Forwards",
		"MIME-Version",
		"Ordering-Type",
		"Origin",
		"Overwrite",
		"Position",
		"Pragma",
		"Prefer",
		"Preference-Applied",
		"Proxy-Authenticate",
		"Proxy-Authentication-Info",
		"Proxy-Authorization",
		"Public-Key-Pins",
		"Public-Key-Pins-Report-Only",
		"Range",
		"Referer",
		"Retry-After",
		"Schedule-Reply",
		"Schedule-Tag",
		"Sec-WebSocket-Accept",
		"Sec-WebSocket-Extensions",
		"Sec-WebSocket-Key",
		"Sec-WebSocket-Protocol",
		"Sec-WebSocket-Version",
		"Server",
		"Set-Cookie",
		"SLUG",
		"Strict-Transport-Security",
		"TE",
		"Timeout",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"User-Agent",
		"Vary",
		"Via",
		"WWW-Authenticate",
		"Warning",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Origin",
		"Access-Control-Max-Age",
		"Access-Control-Request-Method",
		"Access-Control-Request-Headers",
		"Compliance",
		"Content-Transfer-Encoding",
		"Cost",
		"EDIINT-Features",
		"Message-ID",
		"Non-Compliance",
		"Optional",
		"Resolution-Hint",
		"Resolver-Location",
		"SubOK",
		"Subst",
		"Title",
		"UA-Color",
		"UA-Media",
		"UA-Pixels",
		"UA-Resolution",
		"UA-Windowpixels",
		"Version",
		"X-Device-Accept",
		"X-Device-Accept-Charset",
		"X-Device-Accept-Encoding",
		"X-Device-Accept-Language",
		"X-Device-User-Agent",
	} {
		lower := strings.ToLower(v)
		commonHeader[v] = lower
		commonHeader[lower] = lower
	}
}
