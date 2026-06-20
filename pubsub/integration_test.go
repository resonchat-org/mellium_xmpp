// Copyright 2021 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

//go:build integration
// +build integration

package pubsub_test

import (
	"context"
	_ "crypto/sha1"
	"crypto/tls"
	"encoding/xml"
	"iter"
	"slices"
	"strconv"
	"testing"

	"mellium.im/sasl"
	"mellium.im/xmlstream"
	"mellium.im/xmpp"
	"mellium.im/xmpp/crypto"
	"mellium.im/xmpp/disco"
	"mellium.im/xmpp/internal/integration"
	"mellium.im/xmpp/internal/integration/ejabberd"
	"mellium.im/xmpp/internal/integration/prosody"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/mux"
	"mellium.im/xmpp/pubsub"
	"mellium.im/xmpp/stanza"
)

func TestIntegrationPubFetch(t *testing.T) {
	prosodyRun := prosody.Test(context.TODO(), t,
		integration.Log(),
		prosody.Component("pubsub.localhost", "", "pubsub"),
		prosody.ListenC2S(),
	)
	prosodyRun(integrationPubFetch)

	ejabberdRun := ejabberd.Test(context.TODO(), t,
		integration.Log(),
		ejabberd.ListenC2S(),
	)
	ejabberdRun(integrationPubFetch)
}

type handler struct {
	t    *testing.T
	seen map[string]struct{}
}

func (h *handler) Nodes() iter.Seq[string] {
	return slices.Values([]string{h.t.Name()})
}

func (h *handler) Notify(node string) bool {
	return node == h.t.Name()
}

func (h *handler) HandleItems(from jid.JID, node string, iter pubsub.Iter) error {
	defer iter.Close()
	for iter.Next() {
		id, _ := iter.Item()
		h.seen[id] = struct{}{}
	}
	return iter.Err()
}

func integrationPubFetch(ctx context.Context, t *testing.T, cmd *integration.Cmd) {
	j, pass := cmd.User()
	session, err := cmd.DialClient(ctx, j, t,
		xmpp.StartTLS(&tls.Config{
			InsecureSkipVerify: true,
		}),
		xmpp.SASL("", pass, sasl.Plain),
		xmpp.BindResource(),
	)
	if err != nil {
		t.Fatalf("error connecting: %v", err)
	}
	seen := make(map[string]struct{})
	m := mux.New(
		session.In().XMLNS,
		pubsub.HandleEvents(&handler{
			t:    t,
			seen: seen,
		}),
		disco.HandleWithURI("test", crypto.SHA1.New()),
	)
	go func() {
		err := session.Serve(m)
		if err != nil {
			t.Logf("error from serve: %v", err)
		}
	}()
	discoInfo, err := disco.CollectInfo(m, "")
	if err != nil {
		t.Fatalf("failed to collect disco info: %v", err)
	}
	c := disco.Caps{
		Node: "test",
		Hash: crypto.SHA1,
		Ver:  discoInfo.Hash(crypto.SHA1.New()),
	}

	if err := session.Send(ctx, stanza.Presence{}.Wrap(c.TokenReader())); err != nil {
		t.Fatalf("failed send initial presence: %v", err)
	}
	for i := 0; i < 10; i++ {
		strID := strconv.Itoa(i)
		newID, err := pubsub.Publish(ctx, session, t.Name(), strID, xmlstream.Wrap(
			nil,
			xml.StartElement{
				Name: xml.Name{Local: "foo"},
				Attr: []xml.Attr{{Name: xml.Name{Local: "id"}, Value: strID}},
			},
		))
		if err != nil {
			t.Fatalf("error publishing %d: %v", i, err)
		}
		if newID != strID {
			t.Errorf("wrong ID for published value: want=%q, got=%q", strID, newID)
		}
	}
	iter := pubsub.Fetch(ctx, session, pubsub.Query{
		Node: t.Name(),
	})

	const strID = "9"
	hasNext := iter.Next()
	if !hasNext {
		t.Fatalf("no item found")
	}
	id, r := iter.Item()
	if id != strID {
		t.Errorf("wrong ID for fetched value: want=%q, got=%q", strID, id)
	}
	foo := struct {
		XMLName xml.Name `xml:"foo"`
		ID      int      `xml:"id,attr"`
	}{}
	err = xml.NewTokenDecoder(r).Decode(&foo)
	if err != nil {
		t.Fatalf("error decoding %s foo: %v", id, err)
	}
	if foo.ID != 9 {
		t.Errorf("wrong value for ID in element: want=%s, got=%d", strID, foo.ID)
	}
	hasNext = iter.Next()
	if hasNext {
		t.Fatalf("expected default pep to only store one item")
	}

	err = iter.Close()
	if err != nil {
		t.Fatalf("error closing iter: %v", err)
	}

	err = pubsub.Delete(ctx, session, t.Name(), "9", false)
	if err != nil {
		t.Fatalf("error retracting pubsub item: %v", err)
	}

	iter = pubsub.Fetch(ctx, session, pubsub.Query{
		Node: t.Name(),
	})
	if iter.Next() {
		t.Fatalf("expected node to be empty after deletion")
	}
	err = iter.Close()
	if err != nil {
		t.Fatalf("error closing second iter: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, ok := seen[strconv.Itoa(i)]; !ok {
			t.Fatalf("didn't receive notification for item %d", i)
		}
	}
}
