package pubsub_test

import (
	"testing"

	"mellium.im/xmpp/internal/xmpptest"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/pubsub"
)

func TestSubscriptionMarshaling(t *testing.T) {
	addr := jid.MustParse("francisco@denmark.lit")
	xmpptest.RunEncodingTests(t, []xmpptest.EncodingTestCase{
		{
			Value: &pubsub.Subscription{
				ID:           "ba49252aaa4f5d320c24d3766f0bdcade78c78d3",
				Node:         "princely_musings",
				Addr:         addr,
				Subscription: pubsub.SubSubscribed,
			},
			XML: `<pubsub xmlns="http://jabber.org/protocol/pubsub"><subscription node="princely_musings" jid="francisco@denmark.lit" subid="ba49252aaa4f5d320c24d3766f0bdcade78c78d3" subscription="subscribed"></subscription></pubsub>`,
		},
	})
}
