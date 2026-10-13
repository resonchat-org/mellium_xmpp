// Copyright 2026 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package commands

import (
	"encoding/xml"
	"fmt"

	"mellium.im/xmlstream"

	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/disco/info"
	"mellium.im/xmpp/disco/items"
	"mellium.im/xmpp/internal/attr"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/stanza"
)

// Error represents error conditions defined in the
// http://jabber.org/protocol/commands namespace by XEP-0050.
type Error string

const (
	// The responding JID does not understand the specified action.
	MalformedAction Error = "malformed-action"
	// The responding JID cannot accept the specified action.
	BadAction Error = "bad-action"
	// The responding JID cannot accept the specified language/locale.
	BadLocale Error = "bad-locale"
	// The responding JID cannot accept the specified payload (e.g. the
	// data form did not provide one or more required fields).
	BadPayload Error = "bad-payload"
	// The responding JID cannot accept the specified sessionid.
	BadSessionID Error = "bad-sessionid"
	// The requesting JID specified a sessionid that is no longer active
	// (either because it was completed, canceled, or timed out).
	SessionExpired Error = "session-expired"
)

// Error implements the built-in error interface.
func (e Error) Error() string {
	return string(e)
}

// TokenReader implements [mellium.im/xmlstream.Marshaler].
func (e Error) TokenReader() xml.TokenReader {
	return xmlstream.Wrap(
		nil,
		xml.StartElement{
			Name: xml.Name{Space: NS, Local: string(e)},
		},
	)
}

// MarshalXML implements [encoding/xml.Marshaler].
func (e Error) MarshalXML(w *xml.Encoder, _ xml.StartElement) error {
	_, err := xmlstream.Copy(w, e.TokenReader())
	return err
}

// UnmarshalXML implements [encoding/xml.Unmarshaler].
func (e *Error) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	switch Error(start.Name.Local) {
	case MalformedAction:
		*e = MalformedAction
	case BadAction:
		*e = BadAction
	case BadLocale:
		*e = BadLocale
	case BadPayload:
		*e = BadPayload
	case BadSessionID:
		*e = BadSessionID
	case SessionExpired:
		*e = SessionExpired
	default:
		return fmt.Errorf("unknown command execution error: %q", start.Name.Local)
	}
	return nil
}

// insertError inserts the specified error into an error IQ.
func insertError(e Error, r xml.TokenReader) xml.TokenReader {
	return xmlstream.InsertFunc(func(start xml.StartElement, level uint64, w xmlstream.TokenWriter) error {
		if level != 2 || start.Name.Local != "error" {
			return nil
		}
		_, err := xmlstream.Copy(w, e.TokenReader())
		return err
	})(r)
}

// Handle registers the specified ad-hoc commands under the given JID. It then
// handles their execution and session tracking.
func Handle(j jid.JID, commands ...Cmd) mux.Option {
	h := &handler{
		jid: j,
	}
	h.cmds = make(map[string]Cmd)
	for _, cmd := range commands {
		h.cmds[cmd.Node()] = cmd
	}
	h.sessions = make(map[string]Stage)
	return mux.IQ(stanza.SetIQ, xml.Name{Space: NS, Local: "command"}, h)
}

type handler struct {
	cmds     map[string]Cmd
	sessions map[string]Stage
	jid      jid.JID
}

// ForFeatures implements [mellium.im/xmpp/disco/info.FeatureIter].
func (h *handler) ForFeatures(node string, f func(info.Feature) error) error {
	if node == "" {
		return f(Feature)
	}
	for _, cmd := range h.cmds {
		if cmd.Node() == node {
			return f(Feature)
		}
	}
	return nil
}

// ForFeatures implements [mellium.im/xmpp/disco/info.IdentityIter].
func (h *handler) ForIdentities(node string, f func(info.Identity) error) error {
	if node == NS {
		return f(disco.AutomationCommandList)
	}
	for _, cmd := range h.cmds {
		if cmd.Node() == node {
			ident := disco.AutomationCommandNode
			ident.Name = cmd.Name()
			return f(ident)
		}
	}
	return nil
}

// ForFeatures implements [mellium.im/xmpp/disco/item.Iter].
func (h *handler) ForItems(node string, f func(items.Item) error) error {
	if node != NS {
		return nil
	}
	for _, cmd := range h.cmds {
		if err := f(items.Item{
			Node: cmd.Node(),
			Name: cmd.Name(),
			JID:  h.jid,
		}); err != nil {
			return err
		}
	}
	return nil
}

// HandleIQ implements [mellium.im/xmpp/mux.IQHandler].
func (h *handler) HandleIQ(iq stanza.IQ, r xmlstream.TokenReadEncoder, start *xml.StartElement) error {
	var command Command
	if err := xml.NewTokenDecoder(xmlstream.Wrap(xmlstream.Token(nil), *start)).Decode(&command); err != nil {
		return err
	}

	resp := Response{
		Node:   command.Node,
		SID:    command.SID,
		Status: "executing",
	}

	var (
		next    Stage
		payload xml.TokenReader
		err     error
		notes   []Note
	)
	if command.SID == "" {
		cmd, ok := h.cmds[command.Node]
		if !ok {
			_, err := xmlstream.Copy(r, iq.Error(stanza.Error{
				Type:      stanza.Cancel,
				Condition: stanza.ItemNotFound,
			}))
			return err
		}
		next, payload, notes, err = cmd.Execute(iq.Lang)
		resp.SID = attr.RandomID()
	} else {
		stage, ok := h.sessions[command.SID]
		if !ok {
			_, err := xmlstream.Copy(r, insertError(BadSessionID, iq.Error(stanza.Error{
				Type:      stanza.Modify,
				Condition: stanza.BadRequest,
			})))
			return err
		}
		delete(h.sessions, command.SID)

		var action Actions
		switch command.Action {
		case "cancel":
			resp.Status = "canceled"
			payload, notes, err := stage.Cancel()
			for _, note := range notes {
				payload = xmlstream.MultiReader(payload, note.TokenReader())
			}
			// XEP-0050 §2.4.3: The responder MUST reply with the success
			// of the command.
			if _, err := xmlstream.Copy(r, iq.Result(resp.wrapWithoutIQ(payload))); err != nil {
				return err
			}
			return err
		case "next":
			action = Next
		case "prev":
			action = Prev
		case "complete":
			action = Complete
		case "execute", "":
			action = (stage.Actions() & Execute) >> 3
		default:
			_, err := xmlstream.Copy(r, insertError(MalformedAction, iq.Error(stanza.Error{
				Type:      stanza.Modify,
				Condition: stanza.BadRequest,
			})))
			return err
		}
		next, payload, notes, err = stage.Process(action, xmlstream.Inner(r))
	}
	if err != nil {
		if ee, ok := err.(Error); ok {
			var se stanza.Error
			switch ee {
			case SessionExpired:
				se.Type = stanza.Cancel
				se.Condition = stanza.NotAllowed
			default:
				se.Type = stanza.Modify
				se.Condition = stanza.BadRequest
			}
			_, err := xmlstream.Copy(r, insertError(ee, iq.Error(se)))
			return err
		}
		if _, err := xmlstream.Copy(r, iq.Error(stanza.Error{
			Type:      stanza.Cancel,
			Condition: stanza.InternalServerError,
		})); err != nil {
			return err
		}
		return err
	}

	if next == nil {
		resp.Status = "completed"
	} else {
		h.sessions[resp.SID] = next
		payload = xmlstream.MultiReader(next.Actions().TokenReader(), payload)
	}

	for _, note := range notes {
		payload = xmlstream.MultiReader(payload, note.TokenReader())
	}

	_, err = xmlstream.Copy(r, iq.Result(resp.wrapWithoutIQ(payload)))
	return err
}

// Cmd is satisfied by objects that can be executed as ad-hoc commands.
type Cmd interface {
	// Node returns the disco items node uniquely identifying the command.
	Node() string
	// Name returns the human-readable label of the command.
	Name() string
	// Execute executes the command and optionally returns the next stage
	// if this is a multi-stage command. It can also return execution notes
	// and response payload. It takes a single argument corresponding to
	// the language/locale specified by the requester.
	Execute(string) (Stage, xml.TokenReader, []Note, error)
}

// Stage represents further stages of execution of multi-stage commands.
type Stage interface {
	// Process processes the response submitted by the requester.
	Process(action Actions, payload xml.TokenReader) (Stage, xml.TokenReader, []Note, error)
	// Actions specifies which actions are acceptable at this stage of command execution.
	Actions() Actions
	// Cancel aborts the command execution and releases any resources.
	Cancel() (xml.TokenReader, []Note, error)
}
