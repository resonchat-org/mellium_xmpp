// Copyright 2026 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package pubsub

import (
	"encoding/xml"
	"iter"

	"mellium.im/xmlstream"
	"mellium.im/xmpp/disco/info"
	"mellium.im/xmpp/internal/attr"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/paging"
	"mellium.im/xmpp/stanza"
)

var eventName = xml.Name{Local: "event", Space: NSEvent}

// NodeHandler is implemented by objects that advertise which PubSub nodes they
// should be dispatched to handle events for. Such objects should also
// implement the corresponding event type interfaces, such as [ItemsHandler].
type NodeHandler interface {
	// Nodes returns an iterator over node IDs for which the handler should
	// be dispatched.
	Nodes() iter.Seq[string]
}

// NotificationFilterer can be implemented by node handlers that want to
// advertise which nodes they are interested in receiving notifications for via
// the filtered notifications PubSub feature.
type NotificationFilterer interface {
	// Notify signals for which of the handled nodes the handler wants to
	// express interest in receiving notifications for.
	Notify(node string) bool
}

// ItemsHandler is implemented by objects that can handle the items payloads of
// PubSub events.
type ItemsHandler interface {
	// HandleItems receives information about an incoming PubSub items
	// event notification.
	HandleItems(from jid.JID, node string, items Iter) error
}

// HandleEvents returns a mux option to dispatch PubSub events to the provided
// node handlers.
func HandleEvents(handlers ...NodeHandler) mux.Option {
	h := &eventHandler{
		handlers: make(map[string]NodeHandler),
	}
	for _, handler := range handlers {
		for node := range handler.Nodes() {
			h.handlers[node] = handler
		}
	}
	return func(m *mux.ServeMux) {
		mux.Message(stanza.NormalMessage, eventName, h)(m)
		mux.Message(stanza.HeadlineMessage, eventName, h)(m)
	}
}

type eventHandler struct {
	handlers map[string]NodeHandler
}

// ForFeatures implements [mellium.im/disco/info.FeatureIter].
func (h *eventHandler) ForFeatures(node string, f func(info.Feature) error) error {
	if node != "" {
		return nil
	}
	for _, handler := range h.handlers {
		if filterer, ok := handler.(NotificationFilterer); ok {
			for node := range handler.Nodes() {
				if !filterer.Notify(node) {
					continue
				}
				if err := f(info.Feature{Var: node + "+notify"}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// HandleMessage implements [mellium.im/xmpp/mux.MessageHandler].
func (h *eventHandler) HandleMessage(msg stanza.Message, r xmlstream.TokenReadEncoder) error {
	if _, err := r.Token(); err != nil {
		return err
	}
	iter := xmlstream.NewIter(r)
	defer iter.Close()
	for iter.Next() {
		start, payload := iter.Current()
		if start == nil || start.Name != eventName {
			continue
		}
		if err := h.handleEvent(msg, xmlstream.Inner(payload)); err != nil {
			return err
		}
	}
	return iter.Err()
}

func (h *eventHandler) handleEvent(msg stanza.Message, r xml.TokenReader) error {
	iter := xmlstream.NewIter(r)
	defer iter.Close()
	for iter.Next() {
		start, payload := iter.Current()
		if start == nil {
			continue
		}
		_, node := attr.Get(start.Attr, "node")
		switch start.Name.Local {
		case "items":
			if err := h.handleItems(msg, node, xmlstream.Inner(payload)); err != nil {
				return err
			}
		}
	}
	return iter.Err()
}

func (h *eventHandler) handleItems(msg stanza.Message, node string, r xml.TokenReader) error {
	handler, ok := h.handlers[node]
	if !ok {
		return nil
	}
	itemsHandler, ok := handler.(ItemsHandler)
	if !ok {
		return nil
	}
	iter := Iter{iter: paging.NewIter(r, 0)}
	defer iter.Close()
	return itemsHandler.HandleItems(msg.From, node, iter)
}
