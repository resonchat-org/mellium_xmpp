// Copyright 2026 The Mellium Contributors / ResonChat contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

// Package moderation implements XEP-0425: Message Moderation.
//
// Wire shape matches Conversations ModerationManager and ResonChat /
// moxxmpp MessageModerationManager.moderate:
//
//	<iq type='set' to='room@muc.example'>
//	  <moderate xmlns='urn:xmpp:message-moderate:1' id='{stanza-id}'>
//	    <retract xmlns='urn:xmpp:message-retract:1'/>
//	    <reason>…</reason> <!-- optional; ResonChat sends when non-empty -->
//	  </moderate>
//	</iq>
//
// The MUC service then broadcasts a groupchat XEP-0424 <retract/> that
// carries <moderated/>. Clients must not spoof that announcement from an
// occupant JID.
package moderation // import "mellium.im/xmpp/moderation"

import (
	"context"
	"encoding/xml"
	"fmt"

	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/disco/info"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/stanza"
)

// Namespaces (Conversations Namespace.MODERATION / RETRACTION, ResonChat).
const (
	NS        = `urn:xmpp:message-moderate:1`
	NSRetract = `urn:xmpp:message-retract:1`
)

// Feature is the disco feature for XEP-0425.
var Feature = info.Feature{Var: NS}

// Moderate is the IQ payload asking a MUC to retract a message by stanza-id.
type Moderate struct {
	XMLName xml.Name `xml:"urn:xmpp:message-moderate:1 moderate"`
	ID      string   `xml:"id,attr"`
	Reason  string   `xml:"reason,omitempty"`
}

// TokenReader implements xmlstream.Marshaler.
//
// Always includes an empty <retract xmlns='urn:xmpp:message-retract:1'/> as
// Conversations and ResonChat do. Reason is a sibling of retract (current
// XEP-0425 / ResonChat); omitted when empty.
func (m Moderate) TokenReader() xml.TokenReader {
	start := xml.StartElement{
		Name: xml.Name{Space: NS, Local: "moderate"},
		Attr: []xml.Attr{{Name: xml.Name{Local: "id"}, Value: m.ID}},
	}
	parts := []xml.TokenReader{
		xmlstream.Wrap(nil, xml.StartElement{
			Name: xml.Name{Space: NSRetract, Local: "retract"},
		}),
	}
	if m.Reason != "" {
		parts = append(parts, xmlstream.Wrap(
			xmlstream.Token(xml.CharData(m.Reason)),
			xml.StartElement{Name: xml.Name{Local: "reason"}},
		))
	}
	return xmlstream.Wrap(xmlstream.MultiReader(parts...), start)
}

// WriteXML implements xmlstream.WriterTo.
func (m Moderate) WriteXML(w xmlstream.TokenWriter) (n int, err error) {
	return xmlstream.Copy(w, m.TokenReader())
}

// MarshalXML implements xml.Marshaler.
func (m Moderate) MarshalXML(e *xml.Encoder, _ xml.StartElement) error {
	_, err := m.WriteXML(e)
	if err != nil {
		return err
	}
	return e.Flush()
}

// Request sends a moderation IQ to roomBare for the given MUC stanza-id.
// Empty reason omits <reason/> (ResonChat behaviour).
func Request(ctx context.Context, s *xmpp.Session, roomBare jid.JID, stanzaID, reason string) error {
	if s == nil {
		return fmt.Errorf("moderation: nil session")
	}
	if stanzaID == "" {
		return fmt.Errorf("moderation: empty stanza id")
	}
	return s.UnmarshalIQElement(ctx, Moderate{
		ID:     stanzaID,
		Reason: reason,
	}.TokenReader(), stanza.IQ{
		Type: stanza.SetIQ,
		To:   roomBare.Bare(),
	}, nil)
}
