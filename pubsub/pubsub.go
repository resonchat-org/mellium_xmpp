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
	// Skip the initial Message token
	_, err := r.Token()

	if err != nil {
		return err
	}

	iter := xmlstream.NewIter(r)
	for iter.Next() {
		start, child := iter.Current()
		h, ok := s.handlers[start.Name.Space]
		if !ok {
			return fmt.Errorf("Handler for %s not registered.", start.Name.Space)
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
	Subscription   SubType
	Configurable   bool
	ConfigRequired bool
}

// Subscribe creates a new subscription to a namespace.
//
// The namespace should not include the +notify suffix.
// If a subscription already exists for the namespace, Subscribe is a noop.
func (*Subscriptions) Subscribe(ctx context.Context, to jid.JID, session *xmpp.Session, ns, node string, h Handler) (Subscription, error) {
	// Outer IQ set request
	iq := stanza.IQ{
		XMLName: xml.Name{Local: "iq"},
		From:    session.LocalAddr(),
		To:      to,
		Type:    stanza.SetIQ,
	}

	ns = ns + "+notify"

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
		Name: xml.Name{Local: "pubsub", Space: ns},
	})

	respPayload, err := session.SendIQ(ctx, iq.Wrap(pubsubElement))

	if err != nil {
		return Subscription{}, err
	}

	// Response is expected for an IQ set request
	if respPayload == nil {
		return Subscription{}, fmt.Errorf("No response recieved for Subscribe request")
	}

	tok, err := respPayload.Token()
	if err != nil {
		return Subscription{}, err
	}
	start, ok := tok.(xml.StartElement)
	if !ok {
		return Subscription{}, fmt.Errorf("Expected IQ start, but got %T", tok)
	}

	if start.Name.Local != "iq" {
		return Subscription{}, fmt.Errorf("Expected IQ element, but got %s", start.Name.Local)
	}

	_, iqType := attr.Get(start.Attr, "type")

	// Success Case
	if iqType == "result" {
		tok, err := respPayload.Token()
		if err != nil {
			return Subscription{}, err
		}

		// pubsub element
		start, ok = tok.(xml.StartElement)
		if !ok {
			return Subscription{}, fmt.Errorf("Expected pubsub start, but got %T", tok)
		}

		if start.Name.Local != "pubsub" {
			return Subscription{}, fmt.Errorf("Expect pubsub element, but got %s", start.Name.Local)
		}

		_, providedNS := attr.Get(start.Attr, "xmlns")

		if providedNS != ns {
			return Subscription{}, fmt.Errorf("Expected %s namespace, but got %s", ns, providedNS)
		}

		tok, err = respPayload.Token()
		if err != nil {
			return Subscription{}, err
		}

		start, ok = tok.(xml.StartElement)
		if !ok {
			return Subscription{}, fmt.Errorf("Expected subscription start, but got %T", tok)
		}

		if start.Name.Local != "subscription" {
			return Subscription{}, fmt.Errorf("Expected subscription element, but got %s", start.Name.Local)
		}

		// Parse attributes
		errCode, node := attr.Get(start.Attr, "node")
		if errCode == -1 {
			return Subscription{}, fmt.Errorf("Subscription does not have node attribute")
		}
		errCode, jidAttr := attr.Get(start.Attr, "jid")
		if errCode == -1 {
			return Subscription{}, fmt.Errorf("Subscription does not have jid attribute")
		}
		addr, err := jid.Parse(jidAttr)
		if err != nil {
			return Subscription{}, fmt.Errorf("Could not parse jid")
		}
		errCode, subid := attr.Get(start.Attr, "subid")
		if errCode == -1 {
			return Subscription{}, fmt.Errorf("Subscription does not have subid attribute")
		}
		errCode, subscriptionStr := attr.Get(start.Attr, "subscription")
		if errCode == -1 {
			return Subscription{}, fmt.Errorf("Subscription does not have subscription attribute")
		}
		subscription, err := ParseSubType(subscriptionStr)
		if err != nil {
			return Subscription{}, err
		}

		// TODO: Configurable and Config Required Attributes
		return Subscription{
			Node:         node,
			Addr:         addr,
			ID:           subid,
			Subscription: subscription,
		}, nil
	} else if iqType == "error" {
		// TODO: Error Cases
		return Subscription{}, nil
	}

	return Subscription{}, nil
}

func ParseSubType(subscription string) (SubType, error) {
	switch subscription {
	case "subscribed":
		return SubSubscribed, nil
	case "unconfigured":
		return SubUnconfigured, nil
	case "pending":
		return SubPending, nil
	case "none":
		return SubNone, nil
	default:
		return SubNone, fmt.Errorf("Unexpected subscription type %s", subscription)
	}
}

// SubscribeFunc is like Subscribe except it takes a HandlerFunc.
func (*Subscriptions) SubscribeFunc(ctx context.Context, to jid.JID, session *xmpp.Session, ns string, h Handler) (Subscription, error) {
	return Subscription{}, nil
}

// Unsubscribe removes a subscription and stops handling events.
func (*Subscriptions) Unsubscribe(ctx context.Context, ns string) error {
	return nil
}
