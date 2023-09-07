// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package sce_test

import (
	"encoding/xml"
	"fmt"
	"reflect"
	"testing"
	"time"

	"mellium.im/xmlstream"
	"mellium.im/xmpp/jid"
	"mellium.im/xmpp/sce"
)

type envelopeTestCase struct {
	envelope sce.Envelope
	XML      string
	err      error
}

var strictProfile = sce.Profile{
	Must: sce.ProfileFlags{
		Rpad: true,
		Time: true,
		To:   true,
		From: true,
	},
}

var noRpadProfile = sce.Profile{
	Must: sce.ProfileFlags{
		Time: true,
		To:   true,
		From: true,
	},
}

var testJid0 = jid.MustParse("zero@example.com/resource0")
var testJid1 = jid.MustParse("one@example.com/resource1")

var testCasesWrap = [...]envelopeTestCase{
	0: {
		envelope: sce.Envelope{
			Profile: strictProfile,
			From:    testJid0,
		},
		err: sce.ErrProfileMissingTo,
	},
	1: {
		envelope: sce.Envelope{
			Profile: noRpadProfile,
			To:      testJid1,
			From:    testJid0,
			Time:    time.Date(1970, 01, 01, 12, 34, 56, 0, time.UTC),
			Content: xmlstream.Token(xml.CharData("foo")),
		},
		err: nil,
		XML: `<envelope xmlns="urn:xmpp:sce:1">` +
			`<content>foo</content>` +
			`<time>1970-01-01T12:34:56Z</time>` +
			`<to>one@example.com</to>` +
			`<from>zero@example.com</from>` +
			`</envelope>`,
	},
	2: {
		envelope: sce.Envelope{
			Profile: noRpadProfile,
			To:      testJid1,
			From:    testJid0,
			Time:    time.Date(1970, 01, 01, 12, 34, 56, 0, time.UTC),
			Content: xmlstream.Wrap(
				nil,
				xml.StartElement{Name: xml.Name{Local: "foo"}},
			),
		},
		err: nil,
		XML: `<envelope xmlns="urn:xmpp:sce:1">` +
			`<content><foo></foo></content>` +
			`<time>1970-01-01T12:34:56Z</time>` +
			`<to>one@example.com</to>` +
			`<from>zero@example.com</from>` +
			`</envelope>`,
	},
}

func TestWrap(t *testing.T) {
	for i, tc := range testCasesWrap {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			err := tc.envelope.Validate()
			if err != tc.err {
				t.Errorf("err check failed, got=%v, should=%v", err, tc.err)
				return
			}
			if tc.err != nil {
				return
			}
			xb, err := xml.Marshal(tc.envelope)
			if err != nil {
				t.Errorf("xml.Marshal failed: %v", err)
			}
			if xbs := string(xb); xbs != tc.XML {
				t.Errorf("XML check failed, got=%v, should=%v", xbs, tc.XML)
				return
			}
		})
	}
}

var testCasesUnwrap = [...]envelopeTestCase{
	0: {
		envelope: sce.Envelope{
			Profile: strictProfile,
			To:      testJid1.Bare(),
			From:    testJid0.Bare(),
			Time:    time.Date(1970, 01, 01, 12, 34, 56, 0, time.UTC),
			Content: "<foo></foo>",
		},
		err: nil,
		XML: `<envelope xmlns="urn:xmpp:sce:1">` +
			`<content><foo></foo></content>` +
			`<rpad>ABC123</rpad>` +
			`<time>1970-01-01T12:34:56Z</time>` +
			`<to>one@example.com</to>` +
			`<from>zero@example.com</from>` +
			`</envelope>`,
	},
}

func TestUnwrap(t *testing.T) {
	for i, tc := range testCasesUnwrap {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			env := sce.Envelope{Profile: tc.envelope.Profile}
			err := xml.Unmarshal([]byte(tc.XML), &env)
			if err != tc.err {
				t.Errorf("err check failed, got=%v, should=%v", err, tc.err)
				return
			}
			if tc.err != nil {
				return
			}
			if !reflect.DeepEqual(tc.envelope, env) {
				t.Errorf("envelopes didn't match! should: `%#v` is:`%#v`", tc.envelope, env)
				return
			}

		})
	}
}
