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
		mux.Message(stanza.HeadlineMessage, xml.Name{Space: NSOptions}, s)(m)
		mux.Message(stanza.NormalMessage, xml.Name{Space: NSOptions}, s)(m)
		for namespace := range s.handlers {
			mux.Message(stanza.HeadlineMessage, xml.Name{Space: namespace}, s)(m)
			mux.Message(stanza.NormalMessage, xml.Name{Space: NSOptions}, s)(m)
		}
	}
}
