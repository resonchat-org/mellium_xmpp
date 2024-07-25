// Copyright 2021 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package pubsub

import (
	"context"
	"encoding/xml"
	"fmt"

	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/disco/info"
	"mellium.im/xmpp/form"
	"mellium.im/xmpp/internal/attr"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/stanza"
)

type publishResponse struct {
	XMLName xml.Name `xml:"http://jabber.org/protocol/pubsub pubsub"`
	Publish struct {
		Item struct {
			ID string `xml:"id,attr"`
		} `xml:"item"`
	} `xml:"publish"`
}

// Publish copies the first element from the provided token reader to a node on
// the server from which it can be retrieved later.
func Publish(ctx context.Context, s *xmpp.Session, node, id string, item xml.TokenReader) (string, error) {
	return PublishIQ(ctx, s, stanza.IQ{}, node, id, item)
}

// PublishIQ is like Publish except that it allows modifying the IQ.
// Changes to the IQ type will have no effect.
func PublishIQ(ctx context.Context, s *xmpp.Session, iq stanza.IQ, node, id string, item xml.TokenReader) (string, error) {
	iq.Type = stanza.SetIQ
	start, err := item.Token()
	if err != nil {
		return "", err
	}
	itemAttrs := []xml.Attr{}
	if id != "" {
		itemAttrs = append(itemAttrs, xml.Attr{
			Name:  xml.Name{Local: "id"},
			Value: id,
		})
	}
	resp := publishResponse{}
	err = s.UnmarshalIQElement(ctx, xmlstream.Wrap(
		xmlstream.Wrap(
			xmlstream.Wrap(
				xmlstream.MultiReader(xmlstream.Token(start), xmlstream.InnerElement(item)),
				xml.StartElement{Name: xml.Name{Local: "item"}, Attr: itemAttrs},
			),
			xml.StartElement{Name: xml.Name{Local: "publish"}, Attr: []xml.Attr{{Name: xml.Name{Local: "node"}, Value: node}}},
		),
		xml.StartElement{Name: xml.Name{Space: NS, Local: "pubsub"}},
	), iq, &resp)
	if resp.Publish.Item.ID == "" {
		return id, err
	}
	return resp.Publish.Item.ID, err
}

// Subscriptions is a handler that, when registered against a mux with Handle,
// allows the user to subscribe and receive updates to pubsub nodes.
type Subscriptions struct {
	features []info.Feature
	handlers map[string]Handler
}

// Handler responds to pubsub events.
type Handler interface {
	HandleEvent(stanza.Message, Iter) error
}

// ForFeatures implements info.FeatureIter.
func (s *Subscriptions) ForFeatures(node string, f func(info.Feature) error) error {
	if node != "" {
		return nil
	}

	pubsubFeature := info.Feature{Var: NSOptions}

	var err error = f(pubsubFeature)

	if err != nil {
		return err
	}

	for _, feature := range s.features {
		err = f(feature)

		if err != nil {
			return err
		}
	}

	return nil
}

// HandleMessage satisfies mux.MessageHandler.
func (s *Subscriptions) HandleMessage(p stanza.Message, r xmlstream.TokenReadEncoder) error {
	// Skip the initial event token
	_, err := r.Token()

	if err != nil {
		return err
	}

	iter := xmlstream.NewIter(r)
	for iter.Next() {
		start, child := iter.Current()
		idx, node := attr.Get(start.Attr, "node")
		if idx == -1 {
			return fmt.Errorf("node not found in item element")
		}

		h, ok := s.handlers[node]
		if !ok {
			return fmt.Errorf("Handler for %s not registered.", node)
		}

		err = h.HandleEvent(p, Iter{current: child})
		if err != nil {
			return err
		}
	}

	return iter.Err()
}

// Handle returns an option that registers the handler for use with a
// multiplexer.
func Handle(s *Subscriptions) mux.Option {
	return func(m *mux.ServeMux) {
		// Default event namespace registration
		mux.Message(stanza.HeadlineMessage, xml.Name{Space: NSEvent}, s)(m)
		mux.Message(stanza.NormalMessage, xml.Name{Space: NSEvent}, s)(m)

		// External Handlers Registration
		for namespace := range s.handlers {
			mux.Message(stanza.HeadlineMessage, xml.Name{Space: namespace}, s)(m)
			mux.Message(stanza.NormalMessage, xml.Name{Space: namespace}, s)(m)
		}
	}
}

// The HandlerFunc type is an adapter to allow the use of ordinary functions as
// event handlers.
// If f is a function with the appropriate signature, EventHandlerFunc(f) is a
// Handler that calls f.
type HandlerFunc func(stanza.Message, Iter) error

// HandleEvent calls f(msg, iter).
func (f HandlerFunc) HandleEvent(msg stanza.Message, iter Iter) error {
	return f(msg, iter)
}

// Subscription is a description of a particular subscription for which we will
// receive events.
type Subscription struct {
	ID             string
	Node           string
	Addr           jid.JID
	Error          Error
	Subscription   SubType
	Configurable   bool
	ConfigRequired bool
}

// TokenReader satisfies the xmlstream.Marshaler interface.
func (s *Subscription) TokenReader() xml.TokenReader {
	panic("this function is not yet implemented")
}

// WriteXML satisfies the xmlstream.WriterTo interface.
// It is like MarshalXML except it writes tokens to w.
func (s *Subscription) WriteXML(w xmlstream.TokenWriter) (n int, err error) {
	panic("this function is not yet implemented")
}

// MarshalXML implements xml.Marshaler.
func (s *Subscription) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	panic("this function is not yet implemented")
}

// UnmarshalXML implements xml.Unmarshaler.
func (s *Subscription) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	if start.Name.Local != "iq" {
		return fmt.Errorf("Expected IQ element, but got %s", start.Name.Local)
	}

	_, iqType := attr.Get(start.Attr, "type")

	// Success Case
	if iqType == "result" {
		tok, err := d.Token()
		if err != nil {
			return err
		}

		// pubsub element
		start, ok := tok.(xml.StartElement)
		if !ok {
			return fmt.Errorf("Expected pubsub start, but got %T", tok)
		}

		if start.Name.Local != "pubsub" {
			return fmt.Errorf("Expect pubsub element, but got %s", start.Name.Local)
		}

		// _, providedNS := attr.Get(start.Attr, "xmlns")

		// if providedNS != ns {
		// 	return Subscription{}, fmt.Errorf("Expected %s namespace, but got %s", ns, providedNS)
		// }

		tok, err = d.Token()
		if err != nil {
			return err
		}

		start, ok = tok.(xml.StartElement)
		if !ok {
			return fmt.Errorf("Expected subscription start, but got %T", tok)
		}

		if start.Name.Local != "subscription" {
			return fmt.Errorf("Expected subscription element, but got %s", start.Name.Local)
		}

		// Parse attributes
		errCode, node := attr.Get(start.Attr, "node")
		if errCode == -1 {
			return fmt.Errorf("Subscription does not have node attribute")
		}
		errCode, jidAttr := attr.Get(start.Attr, "jid")
		if errCode == -1 {
			return fmt.Errorf("Subscription does not have jid attribute")
		}
		addr, err := jid.Parse(jidAttr)
		if err != nil {
			return fmt.Errorf("Could not parse jid")
		}
		errCode, subid := attr.Get(start.Attr, "subid")
		if errCode == -1 {
			return fmt.Errorf("Subscription does not have subid attribute")
		}
		idx, _ := attr.Get(start.Attr, "subscription")
		if idx == -1 {
			return fmt.Errorf("Subscription does not have subscription attribute")
		}
		subscription := SubType(0)
		err = subscription.UnmarshalXMLAttr(start.Attr[idx])
		if err != nil {
			return err
		}

		// s.handlers[node] = h
		// s.features = append(s.features, info.Feature{Var: ns})

		// TODO: Configurable and Config Required Attributes
		s.Node = node
		s.Addr = addr
		s.ID = subid
		s.Subscription = subscription
		return nil
	} else if iqType == "error" {
		// Error Cases
		tok, err := d.Token()
		if err != nil {
			return err
		}

		start, ok := tok.(xml.StartElement)
		if !ok {
			return fmt.Errorf("Expected error start, but got %T", tok)
		}

		return s.Error.UnmarshalXML(d, start)
	}

	return fmt.Errorf("Unexpected IQ type: %s", iqType)
}

// Subscribe creates a new subscription to a namespace.
//
// The node should not include the +notify suffix.
// If a subscription already exists for the namespace, Subscribe is a noop.
// TODO: Extract Marshaler
func (s *Subscriptions) Subscribe(ctx context.Context, to jid.JID, session *xmpp.Session, node string, h Handler) (Subscription, error) {
	// Outer IQ set request
	iq := stanza.IQ{
		XMLName: xml.Name{Local: "iq"},
		From:    session.LocalAddr(),
		To:      to,
		Type:    stanza.SetIQ,
	}

	// Pubsub and subscribe elements
	pubsubElement := xmlstream.Wrap(xmlstream.Wrap(nil, xml.StartElement{
		Name: xml.Name{Local: "subscribe"},
		Attr: []xml.Attr{
			{
				Name:  xml.Name{Local: "node"},
				Value: node,
			},
			{
				Name:  xml.Name{Local: "jid"},
				Value: to.String(),
			},
		},
	}), xml.StartElement{
		Name: xml.Name{Local: "pubsub", Space: NS},
	})

	resp, err := session.SendIQ(ctx, iq.Wrap(pubsubElement))
	if err != nil {
		return Subscription{}, err
	}
	subscription := Subscription{}
	tok, err := resp.Token()
	if err != nil {
		return Subscription{}, err
	}

	start, ok := tok.(xml.StartElement)
	if !ok {
		return Subscription{}, fmt.Errorf("Expected IQ start element, but got %T", tok)
	}

	err = xml.NewTokenDecoder(resp).DecodeElement(&subscription, &start)
	if err != nil {
		return Subscription{}, err
	}

	s.handlers[node] = h

	return subscription, nil
}

// UnmarshalXMLAttr satisfies xml.UnmarshalerAttr.
func (s *SubType) UnmarshalXMLAttr(attr xml.Attr) error {
	if attr.Name.Local != "subscription" {
		return fmt.Errorf("Expected subscription attribute, but recieved %s", attr.Name.Local)
	}

	switch attr.Value {
	case "subscribed":
		*s = SubSubscribed
	case "unconfigured":
		*s = SubUnconfigured
	case "pending":
		*s = SubPending
	case "none":
		*s = SubNone
	default:
		return fmt.Errorf("Unexpected subscription type %s", attr.Value)
	}

	return nil
}

// MarshalXMLAttr satisfies xml.MarshalerAttr.
func (s *SubType) MarshalXMLAttr(name xml.Name) (xml.Attr, error) {
	attr := xml.Attr{Name: name}

	switch *s {
	case SubNone:
		attr.Value = "none"
	case SubPending:
		attr.Value = "pending"
	case SubUnconfigured:
		attr.Value = "unconfigured"
	case SubSubscribed:
		attr.Value = "subscribed"
	default:
		return xml.Attr{}, fmt.Errorf("Unexpected SubType Value: %s", s)
	}

	return attr, nil
}

// SubscribeFunc is like Subscribe except it takes a HandlerFunc.
func (s *Subscriptions) SubscribeFunc(ctx context.Context, to jid.JID, session *xmpp.Session, node string, h HandlerFunc) (Subscription, error) {
	return s.Subscribe(ctx, to, session, node, h)
}

// Unsubscribe removes a subscription and stops handling events.
func (s *Subscriptions) Unsubscribe(ctx context.Context, to jid.JID, session *xmpp.Session, node string) (Subscription, error) {
	// Outer IQ set request
	iq := stanza.IQ{
		XMLName: xml.Name{Local: "iq"},
		From:    session.LocalAddr(),
		To:      to,
		Type:    stanza.SetIQ,
	}

	// Pubsub and subscribe elements
	pubsubElement := xmlstream.Wrap(xmlstream.Wrap(nil, xml.StartElement{
		Name: xml.Name{Local: "unsubscribe"},
		Attr: []xml.Attr{
			{
				Name:  xml.Name{Local: "node"},
				Value: node,
			},
			{
				Name:  xml.Name{Local: "jid"},
				Value: to.String(),
			},
		},
	}), xml.StartElement{
		Name: xml.Name{Local: "pubsub", Space: NS},
	})

	resp, err := session.SendIQ(ctx, iq.Wrap(pubsubElement))
	if err != nil {
		return Subscription{}, err
	}

	subscription := Subscription{}
	tok, err := resp.Token()
	if err != nil {
		return Subscription{}, err
	}

	start, ok := tok.(xml.StartElement)
	if !ok {
		return Subscription{}, fmt.Errorf("Expected IQ start element, but got %T", tok)
	}

	err = xml.NewTokenDecoder(resp).DecodeElement(&subscription, &start)
	if err != nil {
		return Subscription{}, err
	}

	delete(s.handlers, node)

	return subscription, nil
}

// Error represents an error returned by a pubsub service.
type Error struct {
	Stanza    stanza.Error
	Condition Condition

	// The specific feature that caused the error.
	// Only set if the Condition is Unsupported.
	Feature string

	// The new location of a pubsub node that has been moved.
	// Only set if the Condition is Gone.
	NewURI string

	// The required configuration for the node.
	// Only set if the Condition is ConfigurationRequired.
	Configuration *form.Data
}

// TokenReader satisfies the xmlstream.Marshaler interface.
func (e Error) TokenReader() xml.TokenReader {
	switch e.Condition {
	case CondInvalidJID:
		e.Stanza.Type = stanza.Modify
		e.Stanza.Condition = stanza.BadRequest
		invalidJIDElement := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "invalid-jid"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, invalidJIDElement))
	case CondPresenceRequired:
		e.Stanza.Type = stanza.Auth
		e.Stanza.Condition = stanza.NotAuthorized
		presenceRequiredElement := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "presence-subscription-required"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, presenceRequiredElement))
	case CondNotInRosterGroup:
		e.Stanza.Type = stanza.Auth
		e.Stanza.Condition = stanza.NotAuthorized
		notInRosterElement := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "not-in-roster-group"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, notInRosterElement))
	case CondClosedNode:
		e.Stanza.Type = stanza.Cancel
		e.Stanza.Condition = stanza.NotAllowed
		closedNodeElement := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "closed-node"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, closedNodeElement))
	// TODO: case CondPaymentRequired:
	// TODO: case CondAnonymousSubscriptions:
	case CondPendingSubscription:
		e.Stanza.Type = stanza.Auth
		e.Stanza.Condition = stanza.NotAuthorized
		pendingSubscription := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "pending-subscription"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, pendingSubscription))
	// TODO: case CondBlocked:
	case CondTooManySubscriptions:
		e.Stanza.Type = stanza.Wait
		e.Stanza.Condition = stanza.PolicyViolation
		tooManySubs := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "too-many-subscriptions"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, tooManySubs))
	case CondUnsupported:
		e.Stanza.Type = stanza.Cancel
		e.Stanza.Condition = stanza.FeatureNotImplemented
		unsupported := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "unsupported"},
			Attr: []xml.Attr{
				{
					Name:  xml.Name{Local: "feature"},
					Value: "subscribe",
				},
			},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, unsupported))
	// TODO: case CondGone:
	// TODO: case CondNodeDNE:
	case CondConfigRequired:
		e.Stanza.Type = stanza.Modify
		e.Stanza.Condition = stanza.NotAcceptable
		configrequired := xml.StartElement{
			Name: xml.Name{Space: NSErrors, Local: "configuration-required"},
		}
		return e.Stanza.Wrap(xmlstream.Wrap(nil, configrequired))
	}

	return e.Stanza.Wrap(nil)
}

// WriteXML satisfies the xmlstream.WriterTo interface.
// It is like MarshalXML except it writes tokens to w.
func (e Error) WriteXML(w xmlstream.TokenWriter) (int, error) {
	return xmlstream.Copy(w, e.TokenReader())
}

// MarshalXML implements xml.Marshaler.
func (e Error) MarshalXML(enc *xml.Encoder, _ xml.StartElement) error {
	_, err := e.WriteXML(enc)
	return err
}

// UnmarshalXML implements xml.Unmarshaler.
func (e *Error) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var err error
	e.Stanza, err = stanza.UnmarshalError(xmlstream.Wrap(nil, start))
	if err != nil {
		return err
	}

	switch e.Stanza.Type {
	case stanza.Modify:
		if e.Stanza.Condition == stanza.BadRequest {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			elem, ok := tok.(xml.StartElement)

			if !ok {
				return fmt.Errorf("Unexpected element: %T", tok)
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "invalid-jid"}) {
				e.Condition = CondInvalidJID
				return nil
			}
		}
	case stanza.Auth:
		if e.Stanza.Condition == stanza.NotAuthorized {
			tok, err := d.Token()
			if err != nil {
				return err
			}

			elem, ok := tok.(xml.StartElement)
			if !ok {
				return fmt.Errorf("Unexpected element: %T", tok)
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "presence-subscription-required"}) {
				e.Condition = CondPresenceRequired
				return nil
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "not-in-roster-group"}) {
				e.Condition = CondNotInRosterGroup
				return nil
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "pending-subscription"}) {
				e.Condition = CondPendingSubscription
				return nil
			}
		}
	case stanza.Cancel:
		if e.Stanza.Condition == stanza.NotAllowed {
			tok, err := d.Token()
			if err != nil {
				return err
			}

			elem, ok := tok.(xml.StartElement)
			if !ok {
				return fmt.Errorf("Unexpected element: %T", tok)
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "closed-node"}) {
				e.Condition = CondClosedNode
				return nil
			}
		}

		if e.Stanza.Condition == stanza.FeatureNotImplemented {
			tok, err := d.Token()
			if err != nil {
				return err
			}

			elem, ok := tok.(xml.StartElement)
			if !ok {
				return fmt.Errorf("Unexpected element: %T", tok)
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "unsupported"}) {
				idx, val := attr.Get(elem.Attr, "feature")
				if idx == -1 {
					return fmt.Errorf("Expected field 'feature' not found")
				}

				e.Feature = val

				e.Condition = CondUnsupported
				return nil
			}
		}
	case stanza.Wait:
		if e.Stanza.Condition == stanza.PolicyViolation {
			tok, err := d.Token()
			if err != nil {
				return err
			}

			elem, ok := tok.(xml.StartElement)
			if !ok {
				return fmt.Errorf("Unexpected element: %T", tok)
			}

			if elem.Name == (xml.Name{Space: NSErrors, Local: "too-many-subscriptions"}) {
				e.Condition = CondTooManySubscriptions
				return nil
			}
		}
	}

	return fmt.Errorf("Unable to Unmarshal pubsub error, unexpected error type: %s", e.Stanza.Type)
}

// Error implements the error interface.
func (e Error) Error() string {
	panic("not yet implemented")
}
