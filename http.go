package raven

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
)

func NewHttp(req *http.Request) *Http {
	proto := "http"
	if req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https" {
		proto = "https"
	}
	h := &Http{
		Method:  req.Method,
		Cookies: req.Header.Get("Cookie"),
		Query:   sanitizeQuery(req.URL.Query()).Encode(),
		URL:     proto + "://" + req.Host + req.URL.Path,
		Headers: make(map[string]string, len(req.Header)),
	}
	if addr, port, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		h.Env = map[string]string{"REMOTE_ADDR": addr, "REMOTE_PORT": port}
	}
	for k, v := range req.Header {
		h.Headers[k] = strings.Join(v, ",")
	}
	return h
}

var querySecretFields = []string{"password", "passphrase", "passwd", "secret"}

func sanitizeQuery(query url.Values) url.Values {
	for _, keyword := range querySecretFields {
		for field := range query {
			if strings.Contains(field, keyword) {
				query[field] = []string{"********"}
			}
		}
	}
	return query
}

// https://docs.getsentry.com/hosted/clientdev/interfaces/#context-interfaces
type Http struct {
	// Required
	URL    string `json:"url"`
	Method string `json:"method"`
	Query  string `json:"query_string,omitempty"`

	// Optional
	Cookies string            `json:"cookies,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// Must be either a string or map[string]string
	Data interface{} `json:"data,omitempty"`
}

func (h *Http) Class() string { return "request" }

// RecoveryHandler to wrap the stdlib net/http Mux.
// Example:
//	http.HandleFunc("/", raven.RecoveryHandler(func(w http.ResponseWriter, r *http.Request) {
//		...
//	}))
func RecoveryHandler(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rval := recover(); rval != nil {
				DealWithRecoveredValue(rval, r)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()

		handler(w, r)
	}
}

// DealWithRecoveredValue - helper to do ad-hoc error reporting after recovered panic
// you might want to perform custom cleanup routines yourself
func DealWithRecoveredValue(rval interface{}, r *http.Request) {
	debug.PrintStack()
	err := convertError(rval)
	rvalStr := fmt.Sprint(rval)
	packet := NewPacket(rvalStr, NewException(err, GetOrNewStacktrace(err, 2, 3, nil)), NewHttp(r))
	Capture(packet, nil)
}

// convertError properly converts response values from `recover()` to a Golang error type
// https://stackoverflow.com/questions/19934641/go-returning-from-defer
func convertError(r interface{}) error {
	var res error
	// find out exactly what the error was and set err
	switch x := r.(type) {
	case string:
		res = errors.New(x)
	case error:
		res = x
	default:
		res = errors.New("Unknown panic")
	}
	return res
}
