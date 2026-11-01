// Copyright 2026 The Mellium Contributors / ResonChat contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

// Package sm implements XEP-0198: Stream Management.
//
// Clients typically:
//
//  1. Include [Handler.Feature] in the stream feature list (after SASL).
//  2. After bind (or after a successful resume that sets Ready), call
//     [Handler.Enable] when the session was not resumed.
//  3. Serve with [Handler.Wrap] so inbound stanzas are counted and <r/> is
//     answered with <a/>.
//  4. Prefer [Handler.Encode] / [Handler.Request] for outbound traffic and
//     liveness (Conversations uses <r/> instead of IQ ping when SM is on).
package sm // import "mellium.im/xmpp/sm"

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

// NS is the Stream Management namespace (urn:xmpp:sm:3).
const NS = `urn:xmpp:sm:3`

// Common errors returned by this package.
var (
	ErrNotSupported = errors.New("sm: stream management not advertised")
	ErrFailed       = errors.New("sm: stream management failed")
	ErrNotEnabled   = errors.New("sm: stream management not enabled")
)

// Handler tracks SM counters and answers ack requests.
//
// A single Handler may be reused across reconnects so a resume id survives
// socket loss (Conversations / ResonChat behaviour).
type Handler struct {
	mu sync.Mutex

	supported bool
	enabled   bool
	resumed   bool

	id      string
	bound   jid.JID // full JID from the last successful bind (for resume)
	handled uint32
	sent    uint32
	acked   uint32

	// lastAck is when the most recent <a/> (or enable/resume) arrived.
	lastAck time.Time
}

// Supported reports whether the server advertised SM on the last feature set.
func (h *Handler) Supported() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.supported
}

// Enabled reports whether SM is active on the current stream.
func (h *Handler) Enabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled
}

// Resumed reports whether the current stream was resumed (bind skipped).
func (h *Handler) Resumed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resumed
}

// ID returns the resumable stream id, if any.
func (h *Handler) ID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.id
}

// BoundJID is the full JID from the last successful bind, restored on resume.
func (h *Handler) BoundJID() jid.JID {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bound
}

// RememberBound stores the bound full JID for later resume attempts.
func (h *Handler) RememberBound(addr jid.JID) {
	h.mu.Lock()
	h.bound = addr
	h.mu.Unlock()
}

// HandledCount returns the inbound stanza counter.
func (h *Handler) HandledCount() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.handled
}

// SentCount returns the outbound stanza counter.
func (h *Handler) SentCount() uint32 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sent
}

// LastAck is when the last <a/> (or successful enable/resume) arrived.
func (h *Handler) LastAck() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastAck
}

// MarkHandled increments the inbound counter after a message, iq, or presence
// was received (XEP-0198 §3).
func (h *Handler) MarkHandled() {
	h.mu.Lock()
	h.handled++
	h.mu.Unlock()
}

// MarkSent increments the outbound counter after a stanza was written.
func (h *Handler) MarkSent() {
	h.mu.Lock()
	h.sent++
	h.mu.Unlock()
}

// ResetLive clears per-stream flags after a hard disconnect without wiping the
// resume id (so the next dial can attempt resume).
func (h *Handler) ResetLive() {
	h.mu.Lock()
	h.enabled = false
	h.resumed = false
	h.supported = false
	h.mu.Unlock()
}

// ClearResume drops the resume id and counters (after <failed/> or unrecoverable
// errors).
func (h *Handler) ClearResume() {
	h.mu.Lock()
	h.id = ""
	h.enabled = false
	h.resumed = false
	h.handled = 0
	h.sent = 0
	h.acked = 0
	h.mu.Unlock()
}

func (h *Handler) setEnabled(id string, mayResume bool, now time.Time) {
	h.mu.Lock()
	h.enabled = true
	h.resumed = false
	if mayResume && id != "" {
		h.id = id
	} else {
		h.id = ""
	}
	h.sent = 0
	h.acked = 0
	h.lastAck = now
	h.mu.Unlock()
}

func (h *Handler) setResumed(serverH uint32, now time.Time) {
	h.mu.Lock()
	h.enabled = true
	h.resumed = true
	h.acked = serverH
	h.lastAck = now
	h.mu.Unlock()
}

func (h *Handler) noteAck(serverH uint32, now time.Time) {
	h.mu.Lock()
	h.acked = serverH
	h.lastAck = now
	h.mu.Unlock()
}

// Feature returns the stream feature used during negotiation.
//
// When a resume id is stored, Negotiate attempts <resume/> and may set Ready
// (skipping bind). Otherwise Negotiate is a no-op and the client should call
// [Handler.Enable] after resource bind.
func (h *Handler) Feature() xmpp.StreamFeature {
	return xmpp.StreamFeature{
		Name:       xml.Name{Space: NS, Local: "sm"},
		Necessary:  xmpp.Authn,
		Prohibited: xmpp.Ready,
		List: func(ctx context.Context, e xmlstream.TokenWriter, start xml.StartElement) (bool, error) {
			if err := e.EncodeToken(start); err != nil {
				return false, err
			}
			return false, e.EncodeToken(start.End())
		},
		Parse: func(ctx context.Context, d *xml.Decoder, start *xml.StartElement) (bool, interface{}, error) {
			h.mu.Lock()
			h.supported = true
			h.mu.Unlock()
			return false, nil, d.Skip()
		},
		Negotiate: h.negotiate,
	}
}

func (h *Handler) negotiate(ctx context.Context, session *xmpp.Session, _ interface{}) (xmpp.SessionState, io.ReadWriter, error) {
	h.mu.Lock()
	id := h.id
	handled := h.handled
	h.mu.Unlock()
	if id == "" {
		return 0, nil, nil
	}

	w := session.TokenWriter()
	defer w.Close()
	start := xml.StartElement{
		Name: xml.Name{Space: NS, Local: "resume"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "h"}, Value: strconv.FormatUint(uint64(handled), 10)},
			{Name: xml.Name{Local: "previd"}, Value: id},
		},
	}
	if err := w.EncodeToken(start); err != nil {
		return 0, nil, err
	}
	if err := w.EncodeToken(start.End()); err != nil {
		return 0, nil, err
	}
	if err := w.Flush(); err != nil {
		return 0, nil, err
	}

	r := session.TokenReader()
	defer r.Close()
	d := xml.NewTokenDecoder(r)
	tok, err := d.Token()
	if err != nil {
		return 0, nil, err
	}
	resp, ok := tok.(xml.StartElement)
	if !ok {
		return 0, nil, ErrFailed
	}
	switch {
	case resp.Name.Space == NS && resp.Name.Local == "resumed":
		serverH, _ := parseUintAttr(resp.Attr, "h")
		if err := d.Skip(); err != nil {
			return 0, nil, err
		}
		h.mu.Lock()
		bound := h.bound
		h.mu.Unlock()
		if !bound.Equal(jid.JID{}) {
			session.UpdateAddr(bound)
		}
		h.setResumed(serverH, time.Now())
		return xmpp.Ready, nil, nil
	case resp.Name.Space == NS && resp.Name.Local == "failed":
		_ = d.Skip()
		h.ClearResume()
		return 0, nil, nil
	default:
		_ = d.Skip()
		h.ClearResume()
		return 0, nil, nil
	}
}

// Enable sends <enable resume='true'/> after resource bind.
func (h *Handler) Enable(ctx context.Context, session *xmpp.Session) error {
	if !h.Supported() {
		return ErrNotSupported
	}

	w := session.TokenWriter()
	start := xml.StartElement{
		Name: xml.Name{Space: NS, Local: "enable"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "resume"}, Value: "true"},
		},
	}
	if err := w.EncodeToken(start); err != nil {
		w.Close()
		return err
	}
	if err := w.EncodeToken(start.End()); err != nil {
		w.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	r := session.TokenReader()
	defer r.Close()
	d := xml.NewTokenDecoder(r)
	tok, err := d.Token()
	if err != nil {
		return err
	}
	resp, ok := tok.(xml.StartElement)
	if !ok {
		return ErrFailed
	}
	switch {
	case resp.Name.Space == NS && resp.Name.Local == "enabled":
		id := attrValue(resp.Attr, "id")
		mayResume := attrBool(attrValue(resp.Attr, "resume"))
		if err := d.Skip(); err != nil {
			return err
		}
		h.setEnabled(id, mayResume, time.Now())
		return nil
	case resp.Name.Space == NS && resp.Name.Local == "failed":
		_ = d.Skip()
		return ErrFailed
	default:
		_ = d.Skip()
		return ErrFailed
	}
}

// Request sends <r/> asking the server to acknowledge outbound stanzas.
func (h *Handler) Request(ctx context.Context, session *xmpp.Session) error {
	if !h.Enabled() {
		return ErrNotEnabled
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	w := session.TokenWriter()
	defer w.Close()
	start := xml.StartElement{Name: xml.Name{Space: NS, Local: "r"}}
	if err := w.EncodeToken(start); err != nil {
		return err
	}
	if err := w.EncodeToken(start.End()); err != nil {
		return err
	}
	return w.Flush()
}

// Encode writes v like [xmpp.Session.Encode] and counts it as an outbound
// stanza when SM is enabled.
func (h *Handler) Encode(ctx context.Context, session *xmpp.Session, v interface{}) error {
	if err := session.Encode(ctx, v); err != nil {
		return err
	}
	if h.Enabled() {
		h.MarkSent()
	}
	return nil
}

func (h *Handler) handleR(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	// Serve passes InnerElement(reader): drain any payload then ack.
	_, _ = xmlstream.Copy(xmlstream.Discard(), t)

	h.mu.Lock()
	n := h.handled
	h.mu.Unlock()

	a := xml.StartElement{
		Name: xml.Name{Space: NS, Local: "a"},
		Attr: []xml.Attr{
			{Name: xml.Name{Local: "h"}, Value: strconv.FormatUint(uint64(n), 10)},
		},
	}
	if err := t.EncodeToken(a); err != nil {
		return err
	}
	return t.EncodeToken(a.End())
}

func (h *Handler) handleA(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	serverH, _ := parseUintAttr(start.Attr, "h")
	_, _ = xmlstream.Copy(xmlstream.Discard(), t)
	h.noteAck(serverH, time.Now())
	return nil
}

// Wrap counts inbound message/iq/presence stanzas and dispatches SM nonzas.
func (h *Handler) Wrap(next xmpp.Handler) xmpp.Handler {
	return xmpp.HandlerFunc(func(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
		if start.Name.Space == NS {
			switch start.Name.Local {
			case "r":
				return h.handleR(t, start)
			case "a":
				return h.handleA(t, start)
			}
		}
		if isClientStanza(start.Name) {
			h.MarkHandled()
		}
		return next.HandleXMPP(t, start)
	})
}

func isClientStanza(name xml.Name) bool {
	if name.Local != "message" && name.Local != "iq" && name.Local != "presence" {
		return false
	}
	return name.Space == "" || name.Space == stanza.NSClient
}

func attrBool(v string) bool {
	return v == "true" || v == "1"
}

func parseUintAttr(attrs []xml.Attr, name string) (uint32, bool) {
	for _, a := range attrs {
		if a.Name.Local == name {
			n, err := strconv.ParseUint(a.Value, 10, 32)
			if err != nil {
				return 0, false
			}
			return uint32(n), true
		}
	}
	return 0, false
}

func attrValue(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
