// Copyright 2023 The Mellium Contributors.
// Use of this source code is governed by the BSD 2-clause
// license that can be found in the LICENSE file.

package sce_test

import (
	"crypto/sha256"
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

var (
	DummyKey = []byte("0123456789abcdef0123456789abcdef")
	DummyIV  = []byte("0123456789abcdef")
)

type sceEnvelopeTestCase struct {
	envelope sce.EncryptedEnvelope
	XML      string
	err      error
	header   string
	content  string
}

var testCasesEncrypt = [...]sceEnvelopeTestCase{
	0: {
		envelope: sce.EncryptedEnvelope{
			XMLNamespace:  xml.Name{Space: "foo:bar"},
			AuthedCrypter: sce.NewCBCHMACAuthedCrypter(DummyKey, DummyIV, DummyKey, sha256.New),
			Header: xmlstream.Wrap(
				xmlstream.Wrap(nil, xml.StartElement{Name: xml.Name{Local: "bar"}}),
				xml.StartElement{Name: xml.Name{Local: "header"}},
			),
			Env: sce.Envelope{
				Profile: noRpadProfile,
				To:      testJid1.Bare(),
				From:    testJid0.Bare(),
				Time:    time.Date(1970, 01, 01, 12, 34, 56, 0, time.UTC),
				Content: xmlstream.Wrap(
					nil,
					xml.StartElement{Name: xml.Name{Local: "foo"}},
				),
			},
		},
		err: nil,
		XML: `<encrypted xmlns="foo:bar">` +
			`<header><bar></bar></header>` +
			`<payload>t0BTz+1gUAQcaiOdU0osSfIZzSwyhWm26eX6QzZb0JYv/YU` +
			`4Rvlxp6KCoaXRzwI3fIRtyeySSYthYbqj176U3M9EJGinTLwBoQM1Q9D` +
			`XFmP38j2kVQpE4AcFmbaLGa8jHuU49itpWufZqfbT/9KDDMcoGLi9PJt` +
			`B46/+8ZilvntT30oyLUkeVFCDmTX6LHDxFlt7yJ4o9Z7tCOWUBg3Zkvm` +
			`wXJo3doI1ONi+23mWKjI=` +
			`</payload>` +
			`</encrypted>`,
		header:  "<bar></bar>",
		content: "<foo></foo>",
	},
	1: {
		envelope: sce.EncryptedEnvelope{
			XMLNamespace:  xml.Name{Space: "baz:baan"},
			AuthedCrypter: sce.NewCBCHMACAuthedCrypter(DummyKey, DummyIV, DummyKey, sha256.New),
			Header: xmlstream.Wrap(
				xmlstream.Wrap(nil, xml.StartElement{Name: xml.Name{Local: "bar"}}),
				xml.StartElement{Name: xml.Name{Local: "header"}},
			),
			Env: sce.Envelope{
				Profile: strictProfile,
				To:      testJid1.Bare(),
				From:    testJid0.Bare(),
				Content: xmlstream.Wrap(
					nil,
					xml.StartElement{Name: xml.Name{Local: "foo"}},
				),
			},
		},
		err:     nil,
		header:  "<bar></bar>",
		content: "<foo></foo>",
	},
}

func TestEncryptDecrypt(t *testing.T) {
	for i, tc := range testCasesEncrypt {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			xb, err := xml.Marshal(tc.envelope)
			if err != nil {
				t.Errorf("xml.Marshal failed: %v", err)
			}
			xbs := string(xb)
			if tc.XML != "" && xbs != tc.XML {
				t.Errorf("XML check failed, got=%v, should=%v", xbs, tc.XML)
				return
			}
			mac := make([]byte, sce.OMEMO2_MAC_LENGTH)
			tc.envelope.AuthedCrypter.GetMAC(mac)

			env := sce.XSFOMEMO2SceEnvelope{}
			env.Envelope.AuthedCrypter = sce.NewCBCHMACAuthedCrypter(DummyKey, DummyIV, DummyKey, sha256.New)
			env.Envelope.Env.Profile = tc.envelope.Env.Profile
			env.Envelope.XMLNamespace = tc.envelope.XMLNamespace
			env.Envelope.AuthedCrypter.SetMAC(mac)
			err = xml.Unmarshal([]byte(xbs), &env)
			if err != tc.err {
				t.Errorf("err check failed, got=%v, should=%v", err, tc.err)
				return
			}
			if env.Envelope.Header != tc.header {
				t.Errorf("header check failed, got=%v, should=%v",
					env.Envelope.Header, tc.header)
				return
			}
			tc_env := tc.envelope.Env
			tc_env.Content = tc.content
			if !reflect.DeepEqual(tc_env, env.Envelope.Env) {
				t.Errorf("envelopes didn't match! should: `%#v` is:`%#v`", tc_env, env.Envelope.Env)
				return
			}

		})
	}
}
