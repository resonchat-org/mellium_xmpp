// Copyright 2026 The Mellium Contributors / ResonChat contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package moderation_test

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"mellium.im/xmlstream"
	"mellium.im/xmpp/moderation"
)

func TestModerateMatchesConversationsAndResonChat(t *testing.T) {
	var buf bytes.Buffer
	e := xml.NewEncoder(&buf)
	m := moderation.Moderate{ID: "stanza-id-1", Reason: "Spam"}
	if _, err := xmlstream.Copy(e, m.TokenReader()); err != nil {
		t.Fatal(err)
	}
	_ = e.Flush()
	got := buf.String()
	for _, want := range []string{
		`xmlns="urn:xmpp:message-moderate:1"`,
		`id="stanza-id-1"`,
		`xmlns="urn:xmpp:message-retract:1"`,
		`<reason>Spam</reason>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
}

func TestModerateOmitsEmptyReasonLikeConversations(t *testing.T) {
	// Conversations ModerationManager never sends <reason/>.
	var buf bytes.Buffer
	e := xml.NewEncoder(&buf)
	if _, err := xmlstream.Copy(e, moderation.Moderate{ID: "sid"}.TokenReader()); err != nil {
		t.Fatal(err)
	}
	_ = e.Flush()
	got := buf.String()
	if strings.Contains(got, "reason") {
		t.Fatalf("empty reason should be omitted: %s", got)
	}
	if !strings.Contains(got, `xmlns="urn:xmpp:message-retract:1"`) {
		t.Fatalf("retract child required: %s", got)
	}
}
