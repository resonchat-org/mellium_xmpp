// Copyright 2026 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package bookmarks_test

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"

	"mellium.im/xmlstream"

	"mellium.im/xmpp/bookmarks"
	"mellium.im/xmpp/internal/attr"
	"mellium.im/xmpp/internal/xmpptest"
)

func TestUnparsableJID(t *testing.T) {
	var cs *xmpptest.ClientServer
	cs = xmpptest.NewClientServer(
		xmpptest.ServerHandlerFunc(func(t xmlstream.TokenReadEncoder, start *xml.StartElement) error {
			_, id := attr.Get(start.Attr, "id")
			if id == "" {
				return fmt.Errorf("empty stanza ID")
			}
			if err := cs.Server.Send(context.Background(), xml.NewDecoder(strings.NewReader(fmt.Sprintf(`<iq type="result" id="%s"><pubsub xmlns="http://jabber.org/protocol/pubsub"><items node="urn:xmpp:bookmarks:1"><item id="@invalidjid"><conference xmlns="urn:xmpp:bookmarks:1" name="Invalid JID"/></item></items></pubsub></iq>`, id)))); err != nil {
				return fmt.Errorf("failed to send response: %w", err)
			}
			return nil
		}),
	)
	iter := bookmarks.Fetch(context.Background(), cs.Client)
	if iter.Next() {
		t.Fatal("unexpected valid bookmark received")
	}
	if err := iter.Err(); err == nil {
		t.Fatal("no error returned for invalid bookmark")
	}
}
